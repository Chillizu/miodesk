// Package server wires miodesk's MCP server, widget, and status API together
// and owns their HTTP and stdio lifecycles. MCP protocol handling is the
// official Go SDK's job; miodesk contributes tools and transport hosting.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"miodesk/internal/adapter"
	"miodesk/internal/buildinfo"
	"miodesk/internal/config"
	"miodesk/internal/tools"
	"miodesk/internal/workspace"
	"miodesk/internal/xdg"
	widget "miodesk/web/widget"
)

// Server hosts miodesk's MCP tools for HTTP and stdio clients.
type Server struct {
	cfg     *config.Config
	ws      *workspace.Workspace
	mcp     *mcp.Server
	tools   []ToolInfo
	port    atomic.Int32
	started time.Time

	// Real runtime stats: calls per tool since process start.
	statsMu sync.Mutex
	stats   map[string]int64

	// Recent edit results feed the widget's diff viewer.
	editsMu sync.Mutex
	edits   []EditRecord

	// Long-running command tasks (start/poll/cancel).
	commands *tools.Manager

	auth *Authorize
}

// ToolInfo describes one registered tool for the widget and status output.
type ToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// EditRecord is one applied edit batch, kept for the widget's diff viewer.
type EditRecord struct {
	At    time.Time              `json:"at"`
	Files []tools.EditFileResult `json:"files"`
}

// maxRecentEdits bounds the in-memory edit history.
const maxRecentEdits = 10

// New builds a Server with all tools registered against ws.
func New(cfg *config.Config, ws *workspace.Workspace) *Server {
	s := &Server{
		cfg:      cfg,
		ws:       ws,
		started:  time.Now(),
		stats:    map[string]int64{},
		commands: tools.NewManager(),
	}
	auth, err := NewAuthorize(cfg.Remote.Mode, cfg.Remote.Token)
	if err != nil {
		// cfg.Validate already rejects this; fail closed to local-only.
		slog.Warn("invalid remote access config, using local-only", "err", err)
		auth = &Authorize{mode: AccessLocal}
	}
	s.auth = auth

	s.mcp = mcp.NewServer(
		&mcp.Implementation{
			Name:        "miodesk",
			Title:       "miodesk",
			Description: "Local workspace bridge: sandboxed file tools and commands for AI clients.",
			Version:     buildinfo.Version,
		},
		&mcp.ServerOptions{
			Instructions: "miodesk bridges the user's local workspace. Every file tool (read, search, list, write, edit, delete) is sandboxed inside the workspace root; paths may be relative to the root. Prefer list before deeper reads, and prefer edit over write for changing existing files — edit batches are atomic. Long commands use command_start/command_poll/command_cancel instead of blocking.",
		},
	)
	registerTools(s)
	// The ChatGPT / MCP Apps dashboard is host-facing UI metadata; it lives
	// in the adapter, never in the core tools.
	if err := adapter.Attach(s.mcp, widget.Static, func(ctx context.Context) (any, error) {
		return s.Dashboard(), nil
	}); err != nil {
		// Broken embedded assets are a build error; degrade to text-only
		// rather than refusing to serve.
		slog.Warn("dashboard widget unavailable", "err", err)
	}
	return s
}

func (s *Server) bump(tool string) {
	s.statsMu.Lock()
	s.stats[tool]++
	s.statsMu.Unlock()
}

func (s *Server) recordEdits(files []tools.EditFileResult) {
	s.editsMu.Lock()
	defer s.editsMu.Unlock()
	s.edits = append([]EditRecord{{At: time.Now(), Files: cloneEditFiles(files)}}, s.edits...)
	if len(s.edits) > maxRecentEdits {
		s.edits = s.edits[:maxRecentEdits]
	}
}

func (s *Server) recentEdits() []EditRecord {
	s.editsMu.Lock()
	defer s.editsMu.Unlock()
	return cloneEditRecords(s.edits)
}

// cloneEditRecords returns an immutable snapshot for JSON/API consumers. The
// handler must release editsMu before encoding so a slow client cannot block
// an edit call; copying nested slices also prevents later mutations from
// changing an already-returned dashboard.
func cloneEditRecords(src []EditRecord) []EditRecord {
	if src == nil {
		return nil
	}
	dst := make([]EditRecord, len(src))
	for i := range src {
		dst[i] = src[i]
		dst[i].Files = cloneEditFiles(src[i].Files)
	}
	return dst
}

func cloneEditFiles(src []tools.EditFileResult) []tools.EditFileResult {
	if src == nil {
		return nil
	}
	dst := append([]tools.EditFileResult(nil), src...)
	for i := range dst {
		dst[i].Diff = cloneDiffGroups(src[i].Diff)
	}
	return dst
}

func cloneDiffGroups(src []tools.DiffGroup) []tools.DiffGroup {
	if src == nil {
		return nil
	}
	dst := append([]tools.DiffGroup(nil), src...)
	for i := range dst {
		dst[i].Lines = append([]tools.DiffLine(nil), src[i].Lines...)
	}
	return dst
}

// Dashboard is the payload the adapter's status tool serves and /api/status
// returns: the status fields plus the recent-edit history.
type Dashboard struct {
	Kind string `json:"kind"` // "status"
	Status
	Edits []EditRecord `json:"edits"`
}

func (s *Server) Dashboard() Dashboard {
	edits := s.recentEdits()
	if edits == nil {
		edits = []EditRecord{} // keep the declared array schema honest
	}
	return Dashboard{Kind: "status", Status: s.Status(), Edits: edits}
}

func (s *Server) callCount(tool string) int64 {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	return s.stats[tool]
}

func registerTools(s *Server) {
	s.tools = []ToolInfo{
		{Name: "read", Description: "Read a text file inside the workspace"},
		{Name: "search", Description: "Search file contents inside the workspace"},
		{Name: "list", Description: "List directory entries inside the workspace"},
		{Name: "write", Description: "Create or overwrite a file inside the workspace"},
		{Name: "edit", Description: "Apply atomic text edits to workspace files"},
		{Name: "delete", Description: "Delete a file, symlink, or directory inside the workspace"},
		{Name: "command", Description: "Run a short command inside the workspace"},
		{Name: "command_start", Description: "Start a long-running command in the workspace"},
		{Name: "command_poll", Description: "Poll a long-running command task"},
		{Name: "command_cancel", Description: "Cancel a long-running command task"},
		{Name: "status", Description: "Server status dashboard data"},
	}

	// Every annotation is set explicitly: ChatGPT reads these hints to decide
	// how to frame tool calls.
	readOnly := ann(true, false, false, true)

	// UI metadata comes from the adapter; tools stay host-agnostic.
	declared := func(name string, t *mcp.Tool) *mcp.Tool {
		if meta := adapter.ResultToolMeta(name); meta != nil {
			t.SetMeta(meta)
		}
		return t
	}

	mcp.AddTool(s.mcp, declared("read", &mcp.Tool{
		Name:        "read",
		Title:       "Read file",
		Description: "Read a text file inside the workspace. Supports line-based offset/limit windowing; output is capped at 512 KiB per call.",
		Annotations: readOnly,
	}), func(ctx context.Context, req *mcp.CallToolRequest, in tools.ReadInput) (*mcp.CallToolResult, *tools.ReadOutput, error) {
		s.bump("read")
		out, err := tools.Read(ctx, s.ws, in)
		if err != nil {
			return nil, nil, err
		}
		return nil, out, nil
	})
	mcp.AddTool(s.mcp, declared("search", &mcp.Tool{
		Name:        "search",
		Title:       "Search file contents",
		Description: "Search file contents inside the workspace. Uses ripgrep when available and a built-in engine otherwise.",
		Annotations: readOnly,
	}), func(ctx context.Context, req *mcp.CallToolRequest, in tools.SearchInput) (*mcp.CallToolResult, *tools.SearchOutput, error) {
		s.bump("search")
		out, err := tools.Search(ctx, s.ws, in)
		if err != nil {
			return nil, nil, err
		}
		return nil, out, nil
	})
	mcp.AddTool(s.mcp, declared("list", &mcp.Tool{
		Name:        "list",
		Title:       "List directory",
		Description: "List directory entries inside the workspace with bounded depth and count.",
		Annotations: readOnly,
	}), func(ctx context.Context, req *mcp.CallToolRequest, in tools.ListInput) (*mcp.CallToolResult, *tools.ListOutput, error) {
		s.bump("list")
		out, err := tools.List(ctx, s.ws, in)
		if err != nil {
			return nil, nil, err
		}
		return nil, out, nil
	})
	mcp.AddTool(s.mcp, declared("write", &mcp.Tool{
		Name:        "write",
		Title:       "Write file",
		Description: "Create or overwrite a file inside the workspace. Parent directories are only created when create_dirs is set.",
		Annotations: ann(false, true, false, true),
	}), func(ctx context.Context, req *mcp.CallToolRequest, in tools.WriteInput) (*mcp.CallToolResult, *tools.WriteOutput, error) {
		s.bump("write")
		out, err := tools.Write(ctx, s.ws, in)
		if err != nil {
			return nil, nil, err
		}
		return nil, out, nil
	})
	mcp.AddTool(s.mcp, declared("edit", &mcp.Tool{
		Name:        "edit",
		Title:       "Edit file",
		Description: "Apply exact-replace or guarded range edits to files inside the workspace. Every operation is validated before anything is written, and each file write is atomic (temp + rename).",
		Annotations: ann(false, true, false, true),
	}), func(ctx context.Context, req *mcp.CallToolRequest, in tools.EditInput) (*mcp.CallToolResult, *tools.EditOutput, error) {
		s.bump("edit")
		out, err := tools.Edit(ctx, s.ws, in)
		if err != nil {
			return nil, nil, err
		}
		s.recordEdits(out.Files)
		return nil, out, nil
	})
	mcp.AddTool(s.mcp, declared("delete", &mcp.Tool{
		Name:        "delete",
		Title:       "Delete path",
		Description: "Delete a file, symlink, or directory inside the workspace. Non-empty directories require recursive=true.",
		Annotations: ann(false, true, false, false),
	}), func(ctx context.Context, req *mcp.CallToolRequest, in tools.DeleteInput) (*mcp.CallToolResult, *tools.DeleteOutput, error) {
		s.bump("delete")
		out, err := tools.Delete(ctx, s.ws, in)
		if err != nil {
			return nil, nil, err
		}
		return nil, out, nil
	})
	mcp.AddTool(s.mcp, declared("command", &mcp.Tool{
		Name:        "command",
		Title:       "Run command",
		Description: "Run a short command in the workspace and return stdout, stderr, exit code, and elapsed time.",
		Annotations: ann(false, true, true, false),
	}), func(ctx context.Context, req *mcp.CallToolRequest, in tools.CommandInput) (*mcp.CallToolResult, *tools.CommandOutput, error) {
		s.bump("command")
		out, err := tools.Command(ctx, s.ws, in)
		if err != nil {
			return nil, nil, err
		}
		return nil, out, nil
	})
	mcp.AddTool(s.mcp, declared("command_start", &mcp.Tool{
		Name:        "command_start",
		Title:       "Start long command",
		Description: "Start a long-running command in the workspace; returns a task id for polling and cancellation.",
		Annotations: ann(false, true, true, false),
	}), func(ctx context.Context, req *mcp.CallToolRequest, in tools.CommandInput) (*mcp.CallToolResult, *tools.TaskStarted, error) {
		s.bump("command_start")
		id, err := s.commands.Start(s.ws, in)
		if err != nil {
			return nil, nil, err
		}
		return nil, &tools.TaskStarted{Kind: "command", ID: id, Command: in.Command}, nil
	})
	mcp.AddTool(s.mcp, declared("command_poll", &mcp.Tool{
		Name:        "command_poll",
		Title:       "Poll long command",
		Description: "Poll a long-running command task by id for status and accumulated output.",
		Annotations: ann(true, false, false, true),
	}), func(ctx context.Context, req *mcp.CallToolRequest, in tools.TaskID) (*mcp.CallToolResult, *tools.PollOutput, error) {
		s.bump("command_poll")
		out, err := s.commands.Poll(in.ID)
		if err != nil {
			return nil, nil, err
		}
		return nil, out, nil
	})
	mcp.AddTool(s.mcp, declared("command_cancel", &mcp.Tool{
		Name:        "command_cancel",
		Title:       "Cancel long command",
		Description: "Cancel a long-running command task by id.",
		Annotations: ann(false, true, false, true),
	}), func(ctx context.Context, req *mcp.CallToolRequest, in tools.TaskID) (*mcp.CallToolResult, *tools.PollOutput, error) {
		s.bump("command_cancel")
		out, err := s.commands.Cancel(in.ID)
		if err != nil {
			return nil, nil, err
		}
		return nil, out, nil
	})
}

// ann builds ToolAnnotations with every hint set explicitly. The bool
// pointers are taken on copies so each tool gets its own values.
func ann(readOnly, destructive, openWorld, idempotent bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		ReadOnlyHint:    readOnly,
		DestructiveHint: &destructive,
		OpenWorldHint:   &openWorld,
		IdempotentHint:  idempotent,
	}
}

// Tools returns metadata about the registered tools.
func (s *Server) Tools() []ToolInfo { return s.tools }

// AccessMode reports the active trust level for CLI/doctor output.
func (s *Server) AccessMode() AccessMode { return s.auth.Mode() }

// Handler assembles the HTTP surface: MCP at /mcp, widget at /, status at
// /api/status, recent edits at /api/edits, health at /healthz.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	// The current MCP HTTP protocol (2026-07-28) is sessionless. JSON responses
	// also keep the endpoint compatible with free quick tunnels that do not
	// support server-sent events; clients still advertise both accepted media
	// types as required by Streamable HTTP.
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s.mcp }, &mcp.StreamableHTTPOptions{
		Stateless:                  true,
		JSONResponse:               true,
		DisableLocalhostProtection: s.auth.Mode() != AccessLocal,
	})
	mux.Handle("/mcp", mcpHandler)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s.Dashboard())
	})
	// /preview is a development view: every result renderer with mock data,
	// no MCP involved. kind picks one payload; "all" stacks every mock.
	mux.HandleFunc("GET /preview", func(w http.ResponseWriter, r *http.Request) {
		kind := r.URL.Query().Get("kind")
		if kind == "" {
			kind = "all"
		}
		if !regexp.MustCompile("^[a-z0-9-]{0,32}$").MatchString(kind) {
			http.Error(w, "bad kind", http.StatusBadRequest)
			return
		}
		html, err := adapter.PreviewHTML(widget.Static, kind)
		if err != nil {
			http.Error(w, "widget unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	})
	mux.HandleFunc("GET /widget", func(w http.ResponseWriter, _ *http.Request) {
		html, err := adapter.WidgetHTML(widget.Static)
		if err != nil {
			http.Error(w, "widget unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	})
	mux.HandleFunc("GET /api/edits", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		edits := s.recentEdits()
		if edits == nil {
			edits = []EditRecord{}
		}
		_ = json.NewEncoder(w).Encode(edits)
	})
	if static, err := fs.Sub(widget.Static, "static"); err == nil {
		mux.Handle("/", http.FileServerFS(static))
	}
	return s.auth.Middleware(mux)
}

// ToolCalls is the real usage data behind the widget's token-stats strip.
type ToolCalls struct {
	Calls map[string]int64 `json:"calls"`
	Total int64            `json:"total"`
}

// Status is the JSON payload behind /api/status and the widget.
type Status struct {
	Name          string     `json:"name"`
	Version       string     `json:"version"`
	Platform      string     `json:"platform"`
	Workspace     string     `json:"workspace"`
	Endpoint      string     `json:"endpoint"`
	Port          int        `json:"port"`
	Theme         string     `json:"theme"`
	Remote        string     `json:"remote"`
	StartedAt     time.Time  `json:"started_at"`
	UptimeSeconds int64      `json:"uptime_seconds"`
	Tools         []ToolInfo `json:"tools"`
	Stats         ToolCalls  `json:"stats"`
}

func (s *Server) Status() Status {
	s.statsMu.Lock()
	calls := make(map[string]int64, len(s.stats))
	total := int64(0)
	for name, n := range s.stats {
		calls[name] = n
		total += n
	}
	s.statsMu.Unlock()

	theme := s.cfg.Widget.Theme
	if theme == "" {
		theme = "auto"
	}
	remote := string(s.auth.Mode())
	return Status{
		Name:          "miodesk",
		Version:       buildinfo.Version,
		Platform:      buildinfo.Platform(),
		Workspace:     s.ws.Root(),
		Endpoint:      s.MCPURL(),
		Port:          int(s.port.Load()),
		Theme:         theme,
		Remote:        remote,
		StartedAt:     s.started,
		UptimeSeconds: int64(time.Since(s.started).Seconds()),
		Tools:         s.tools,
		Stats:         ToolCalls{Calls: calls, Total: total},
	}
}

// PortInUseError reports a busy listen port, letting the CLI add a hint.
type PortInUseError struct{ Port int }

func (e *PortInUseError) Error() string {
	return fmt.Sprintf("port %d is already in use", e.Port)
}

// Listen binds the configured host:port. The returned listener is handed to
// Serve, which owns the runtime state file.
func (s *Server) Listen() (net.Listener, error) {
	addr := net.JoinHostPort(s.cfg.Server.Host, strconv.Itoa(s.cfg.Server.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return nil, &PortInUseError{Port: s.cfg.Server.Port}
		}
		return nil, fmt.Errorf("listen on %s: %w", addr, err)
	}
	s.port.Store(int32(ln.Addr().(*net.TCPAddr).Port))
	return ln, nil
}

// Serve runs the HTTP server until ctx is cancelled, then shuts down
// gracefully. The state file lives exactly as long as the server does.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	s.writeState()

	hs := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second}
	serveErr := make(chan error, 1)
	go func() { serveErr <- hs.Serve(ln) }()

	select {
	case err := <-serveErr:
		s.commands.Shutdown()
		s.clearState()
		return err
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = hs.Shutdown(shutCtx)
		s.commands.Shutdown()
		s.clearState()
		return nil
	}
}

// RunStdio serves MCP over stdin/stdout for local clients. Protocol messages
// use stdout; anything human-facing must go to stderr.
func (s *Server) RunStdio(ctx context.Context) error {
	defer s.commands.Shutdown()
	return s.mcp.Run(ctx, &mcp.StdioTransport{})
}

// URL is the widget/status base URL, for display after Listen.
func (s *Server) URL() string    { return s.baseURL("") }
func (s *Server) MCPURL() string { return s.baseURL("/mcp") }

func (s *Server) baseURL(path string) string {
	port := int(s.port.Load())
	if port == 0 {
		return "" // not listening yet
	}
	host := s.cfg.Server.Host
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s%s", net.JoinHostPort(host, strconv.Itoa(port)), path)
}

type stateFile struct {
	PID       int    `json:"pid"`
	Port      int    `json:"port"`
	URL       string `json:"url"`
	StartedAt string `json:"started_at"`
}

func (s *Server) statePath() (string, error) {
	dir, err := xdg.StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "server.json"), nil
}

// writeState records the running server for `miodesk status`. Failures are
// non-fatal: status is a convenience, not a dependency.
func (s *Server) writeState() {
	path, err := s.statePath()
	if err != nil {
		return
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	// The state file contains only PID/port metadata, but it is still a local
	// control-plane detail. Repair permissions when an older installation used
	// a permissive umask.
	if err := os.Chmod(dir, 0o700); err != nil {
		return
	}
	data, err := json.Marshal(stateFile{
		PID:       os.Getpid(),
		Port:      int(s.port.Load()),
		URL:       s.URL(),
		StartedAt: s.started.Format(time.RFC3339),
	})
	if err == nil {
		f, ferr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if ferr != nil {
			return
		}
		defer f.Close()
		if ferr = f.Chmod(0o600); ferr == nil {
			_, ferr = f.Write(data)
		}
		if ferr == nil {
			ferr = f.Sync()
		}
	}
}

func (s *Server) clearState() {
	if path, err := s.statePath(); err == nil {
		_ = os.Remove(path)
	}
}
