package extplugins

// Package extplugins loads explicit, owner-controlled stdio MCP plugins.
// A plugin executes under the same OS user as miodesk: manifests are trusted
// executable configuration, NOT an OS sandbox or an isolation boundary.
import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type Grant struct {
	Name       string `json:"name"`
	Permission string `json:"permission"` // read | write; read only by default
}
type Manifest struct {
	ID      string   `json:"id"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Tools   []Grant  `json:"tools"`
}
type Tool struct {
	PluginID string
	Name     string
	Grant    Grant
	Source   *mcp.Tool
	Session  *mcp.ClientSession
}
type Plugin struct {
	ID    string   `json:"id"`
	Tools []string `json:"tools"`
}
type Manager struct {
	mu       sync.Mutex
	sessions []*mcp.ClientSession
	Tools    []Tool
	Plugins  []Plugin
}

var ident = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
var toolIdent = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]{0,63}$`)

func validateManifest(m Manifest, allowWrites bool) error {
	if !ident.MatchString(m.ID) {
		return errors.New("invalid plugin id")
	}
	if !filepath.IsAbs(m.Command) {
		return errors.New("command must be absolute")
	}
	if len(m.Tools) == 0 || len(m.Tools) > 64 {
		return errors.New("invalid tools allowlist length")
	}
	seen := map[string]bool{}
	for _, g := range m.Tools {
		if !toolIdent.MatchString(g.Name) || seen[g.Name] {
			return errors.New("invalid or repeated tool grant")
		}
		seen[g.Name] = true
		if g.Permission != "read" && g.Permission != "write" {
			return errors.New("grant permission must be read or write")
		}
		if g.Permission == "write" && !allowWrites {
			return errors.New("plugin requires plugins.allow_writes")
		}
	}
	if len(m.Args) > 32 {
		return errors.New("too many arguments")
	}
	return nil
}
func privateFile(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("manifest must be regular file")
	}
	// A writable manifest can select an arbitrary OS command, so reject
	// group/world writable files.
	if info.Mode().Perm()&0022 != 0 {
		return errors.New("manifest is writable by group or others")
	}
	return nil
}
func loadManifest(path string, allowWrites bool) (Manifest, error) {
	if err := privateFile(path); err != nil {
		return Manifest{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return Manifest{}, err
	}
	defer f.Close()
	var m Manifest
	d := json.NewDecoder(io.LimitReader(f, 64<<10))
	d.DisallowUnknownFields()
	if err = d.Decode(&m); err != nil {
		return Manifest{}, err
	}
	var extra any
	if err = d.Decode(&extra); err != io.EOF {
		return Manifest{}, errors.New("trailing manifest data")
	}
	if err = validateManifest(m, allowWrites); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

// Load starts ONLY allowlisted plugins from a trusted directory. Failures are
// isolated: a broken optional plugin cannot prevent core startup.
func Load(ctx context.Context, directory string, allowWrites bool) *Manager {
	mgr := &Manager{}
	if directory == "" {
		slog.Warn("plugins_disabled", "reason", "directory not configured")
		return mgr
	}
	dir, err := os.Lstat(directory)
	if err != nil || !dir.IsDir() || dir.Mode().Perm()&0022 != 0 {
		slog.Warn("plugin_directory_refused", "reason", "missing, non-directory or writable by others")
		return mgr
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		slog.Warn("plugin_directory_unreadable")
		return mgr
	}
	ids := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		manifest, err := loadManifest(filepath.Join(directory, entry.Name()), allowWrites)
		if err != nil {
			slog.Warn("plugin_manifest_refused", "file", entry.Name(), "reason", err.Error())
			continue
		}
		if ids[manifest.ID] {
			slog.Warn("plugin_duplicate_id", "plugin", manifest.ID)
			continue
		}
		ids[manifest.ID] = true
		mgr.loadOne(ctx, manifest)
	}
	return mgr
}
func (mgr *Manager) loadOne(ctx context.Context, m Manifest) {
	// Never use shell; arguments are literal (no expansion).
	cmd := exec.Command(m.Command, m.Args...)
	// Do not give external processes the Core's tunnel/runtime credentials or
	// shell environment by inheritance. Plugin-specific secrets use protected
	// files owned by that plugin, never environment injection.
	for _, key := range []string{"HOME", "USER", "PATH", "LANG", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		if value, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	cmd.Stderr = os.Stderr
	// The plugin inherits the current OS permissions, but not stdin/stdout
	// access beyond the MCP pipes owned by CommandTransport.
	client := mcp.NewClient(&mcp.Implementation{Name: "miodesk-extplugins", Version: "1"}, nil)
	bootCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	session, err := client.Connect(bootCtx, &mcp.CommandTransport{Command: cmd, TerminateDuration: time.Second}, nil)
	if err != nil {
		slog.Warn("plugin_connect_failed", "plugin", m.ID, "reason", err.Error())
		return
	}
	mgr.sessions = append(mgr.sessions, session)
	byName := map[string]*mcp.Tool{}
	var cursor string
	for {
		page, err := session.ListTools(bootCtx, &mcp.ListToolsParams{Cursor: cursor})
		if err != nil {
			slog.Warn("plugin_discovery_failed", "plugin", m.ID)
			return
		}
		for _, t := range page.Tools {
			byName[t.Name] = t
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
		if len(byName) > 256 {
			slog.Warn("plugin_too_many_tools", "plugin", m.ID)
			return
		}
	}
	exported := Plugin{ID: m.ID}
	for _, g := range m.Tools {
		t := byName[g.Name]
		if t == nil {
			slog.Warn("plugin_missing_tool", "plugin", m.ID, "tool", g.Name)
			continue
		}
		// A read grant cannot downgrade a remotely advertised mutating tool.
		// Remote annotations may veto a read grant but never confer privilege.
		if !grantMatchesTool(g, t) {
			slog.Warn("plugin_read_grant_refused", "plugin", m.ID, "tool", g.Name)
			continue
		}
		exposed := m.ID + "__" + g.Name
		if mgr.hasAlias(exposed) {
			slog.Warn("plugin_tool_name_collision", "plugin", m.ID, "tool", g.Name)
			continue
		}
		mgr.Tools = append(mgr.Tools, Tool{PluginID: m.ID, Name: exposed, Grant: g, Source: t, Session: session})
		exported.Tools = append(exported.Tools, exposed)
	}
	sort.Strings(exported.Tools)
	mgr.Plugins = append(mgr.Plugins, exported)
}
func grantMatchesTool(g Grant, t *mcp.Tool) bool {
	return g.Permission == "write" || (t.Annotations != nil && t.Annotations.ReadOnlyHint)
}
func (mgr *Manager) hasAlias(name string) bool {
	for _, t := range mgr.Tools {
		if t.Name == name {
			return true
		}
	}
	return false
}

func (mgr *Manager) Close() {
	mgr.mu.Lock()
	sessions := mgr.sessions
	mgr.sessions = nil
	mgr.mu.Unlock()
	for _, s := range sessions {
		_ = s.Close()
	}
}

// Call forwards only allowlisted tools. Both the manifest grant and the
// plugin's advertised read-only hint are checked before read tool exposure.
// This checks declared behavior, NOT process isolation: review plugin source.
func (t Tool) Call(ctx context.Context, args json.RawMessage) (*mcp.CallToolResult, error) {
	if len(args) > 64<<10 {
		return nil, errors.New("plugin arguments exceed 64 KiB")
	}
	if len(args) == 0 {
		args = []byte("{}")
	}
	var obj map[string]any
	if err := json.Unmarshal(args, &obj); err != nil || obj == nil {
		return nil, errors.New("plugin arguments must be a JSON object")
	}
	runCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	res, err := t.Session.CallTool(runCtx, &mcp.CallToolParams{Name: t.Grant.Name, Arguments: obj})
	if err != nil {
		return nil, fmt.Errorf("external plugin invocation failed: %w", err)
	}
	encoded, err := json.Marshal(res)
	if err != nil {
		return nil, errors.New("invalid plugin result")
	}
	if len(encoded) > 1<<20 {
		return nil, errors.New("plugin result exceeds 1 MiB")
	}
	return res, nil
}
