package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Chillizu/miodesk/internal/adapter"
	"github.com/Chillizu/miodesk/internal/config"
	"github.com/Chillizu/miodesk/internal/logging"
	"github.com/Chillizu/miodesk/internal/tools"
	"github.com/Chillizu/miodesk/internal/workspace"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	// Keep runtime state inside the test sandbox.
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "hello.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Workspace.Root = root
	cfg.Server.Port = 0
	return New(cfg, ws)
}

func TestMCPRoundtripOverHTTP(t *testing.T) {
	s := newTestServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "miodesk-test", Version: "0"}, nil)
	sess, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             ts.URL + "/mcp",
		HTTPClient:           ts.Client(),
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer sess.Close()

	listed, err := sess.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	names := map[string]bool{}
	for _, tl := range listed.Tools {
		names[tl.Name] = true
	}
	for _, want := range []string{"read", "search", "list"} {
		if !names[want] {
			t.Errorf("tool %q missing from %v", want, names)
		}
	}

	call, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "read",
		Arguments: map[string]any{"path": "hello.txt"},
	})
	if err != nil {
		t.Fatalf("tools/call read: %v", err)
	}
	if call.IsError {
		t.Fatalf("read returned tool error: %v", call.Content)
	}
	out := structuredRead(t, call)
	if out.Content != "hello\n" || out.Lines != 1 {
		t.Errorf("structured read output = %+v", out)
	}

	// Sandbox escape becomes a tool error, never a result.
	call, err = sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "read",
		Arguments: map[string]any{"path": "../../../etc/passwd"},
	})
	if err != nil {
		t.Fatalf("tools/call escape: %v", err)
	}
	if !call.IsError {
		t.Error("escape path must produce an error result")
	}
}

func TestMCPCurrentProtocolStatelessJSON(t *testing.T) {
	s := newTestServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"_meta": map[string]any{
				mcp.MetaKeyProtocolVersion:    "2026-07-28",
				mcp.MetaKeyClientInfo:         map[string]any{"name": "miodesk-test", "version": "1"},
				mcp.MetaKeyClientCapabilities: map[string]any{},
			},
			"name":      "status",
			"arguments": map[string]any{},
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", strings.NewReader(string(payload)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	req.Header.Set("Mcp-Method", "tools/call")
	req.Header.Set("Mcp-Name", "status")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("current protocol status = %d, body = %s", resp.StatusCode, data)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Errorf("current protocol content type = %q, want application/json", got)
	}
	var result struct {
		Result struct {
			StructuredContent map[string]any `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Result.StructuredContent["kind"] != "status" {
		t.Errorf("structured content = %v", result.Result.StructuredContent)
	}
}

func TestHandlerLogsRequestsAndToolsWithoutPayloads(t *testing.T) {
	previous := slog.Default()
	defer slog.SetDefault(previous)
	var logs bytes.Buffer
	if err := logging.Configure(logging.Settings{Level: "info", Format: "json"}, &logs); err != nil {
		t.Fatal(err)
	}

	s := newTestServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("X-Miodesk-Request-ID") == "" {
		t.Error("HTTP response should carry a request id")
	}

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "logging-test", Version: "0"}, nil)
	sess, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             ts.URL + "/mcp",
		HTTPClient:           ts.Client(),
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	call, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "read",
		Arguments: map[string]any{"path": "hello.txt"},
	})
	if err != nil || call.IsError {
		t.Fatalf("read: err=%v call=%v", err, call)
	}

	output := logs.String()
	for _, want := range []string{"http_request", "mcp_tool_complete", "\"tool\":\"read\"", "\"request_id\"", "\"duration_ms\""} {
		if !strings.Contains(output, want) {
			t.Errorf("logs missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "hello") {
		t.Errorf("file content leaked into logs:\n%s", output)
	}
}

func TestHandlerLogsAuthDenialWithoutCredential(t *testing.T) {
	previous := slog.Default()
	defer slog.SetDefault(previous)
	var logs bytes.Buffer
	if err := logging.Configure(logging.Settings{Level: "info", Format: "json"}, &logs); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Workspace.Root = t.TempDir()
	cfg.Remote.Mode = "token"
	cfg.Remote.Token = "test-secret-token"
	ws, err := workspace.New(cfg.Workspace.Root)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(New(cfg, ws).Handler())
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	output := logs.String()
	for _, want := range []string{"auth_denied", "missing_or_invalid_bearer", "http_request"} {
		if !strings.Contains(output, want) {
			t.Errorf("logs missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, cfg.Remote.Token) {
		t.Error("bearer token leaked into auth logs")
	}
}

func TestMCPRemoteForwardedHost(t *testing.T) {
	root := t.TempDir()
	ws, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}

	makeRequest := func(t *testing.T, endpoint string) *http.Request {
		t.Helper()
		body := map[string]any{
			"jsonrpc": "2.0",
			"id":      1,
			"method":  "tools/call",
			"params": map[string]any{
				"_meta": map[string]any{
					mcp.MetaKeyProtocolVersion:    "2026-07-28",
					mcp.MetaKeyClientInfo:         map[string]any{"name": "miodesk-test", "version": "1"},
					mcp.MetaKeyClientCapabilities: map[string]any{},
				},
				"name":      "status",
				"arguments": map[string]any{},
			},
		}
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(string(payload)))
		if err != nil {
			t.Fatal(err)
		}
		req.Host = "public.example"
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("MCP-Protocol-Version", "2026-07-28")
		req.Header.Set("Mcp-Method", "tools/call")
		req.Header.Set("Mcp-Name", "status")
		return req
	}

	t.Run("local retains SDK localhost protection", func(t *testing.T) {
		ts := httptest.NewServer(newTestServer(t).Handler())
		defer ts.Close()
		resp, err := ts.Client().Do(makeRequest(t, ts.URL+"/mcp"))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("local forwarded host status = %d, want 403", resp.StatusCode)
		}
	})

	t.Run("unsafe remote accepts tunnel host", func(t *testing.T) {
		cfg := config.Default()
		cfg.Workspace.Root = root
		cfg.Remote.Mode = "unsafe"
		ts := httptest.NewServer(New(cfg, ws).Handler())
		defer ts.Close()
		resp, err := ts.Client().Do(makeRequest(t, ts.URL+"/mcp"))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			data, _ := io.ReadAll(resp.Body)
			t.Errorf("unsafe remote forwarded host status = %d, body = %s", resp.StatusCode, data)
		}
	})
}

func structuredRead(t *testing.T, call *mcp.CallToolResult) tools.ReadOutput {
	t.Helper()
	data, err := json.Marshal(call.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var out tools.ReadOutput
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal structured content %s: %v", data, err)
	}
	return out
}

func TestListenServeShutdown(t *testing.T) {
	s := newTestServer(t)
	ln, err := s.Listen()
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	ctx, cancel := context.WithCancel(context.Background())
	serveDone := make(chan error, 1)
	go func() { serveDone <- s.Serve(ctx, ln) }()

	base := "http://127.0.0.1:" + strconv.Itoa(port)
	resp, err := http.Get(base + "/healthz")
	if err != nil {
		t.Fatalf("healthz: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz = %d", resp.StatusCode)
	}
	if got := resp.Header.Get(HealthHeader); got != HealthHeaderValue {
		t.Errorf("healthz %s = %q, want %q", HealthHeader, got, HealthHeaderValue)
	}

	resp, err = http.Get(base + "/")
	if err != nil {
		t.Fatalf("widget: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "<title>miodesk</title>") {
		t.Error("widget index not served at /")
	}

	resp, err = http.Get(base + "/api/status")
	if err != nil {
		t.Fatalf("api/status: %v", err)
	}
	statusBody, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	var status LocalStatus
	if err := json.Unmarshal(statusBody, &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status.Kind != "status" || status.Name != "miodesk" || status.Port != port || len(status.Tools) != 11 {
		t.Errorf("status = %+v", status)
	}
	if bytes.Contains(statusBody, []byte(`"edits"`)) {
		t.Errorf("api/status should not duplicate edit history: %s", statusBody)
	}
	if status.Endpoint == "" {
		t.Error("status endpoint should be set while listening")
	}

	cancel()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Errorf("Serve returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not stop after context cancel")
	}

	// Runtime state must be cleaned up on shutdown.
	if _, err := os.Stat(filepath.Join(os.Getenv("XDG_STATE_HOME"), "miodesk", "server.json")); !os.IsNotExist(err) {
		t.Error("server.json should be removed after shutdown")
	}
}

func TestListenOccupiedPort(t *testing.T) {
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()

	s := newTestServer(t)
	s.cfg.Server.Port = blocker.Addr().(*net.TCPAddr).Port
	_, err = s.Listen()
	var portErr *PortInUseError
	if !errors.As(err, &portErr) {
		t.Fatalf("Listen error = %v, want *PortInUseError", err)
	}
	if !strings.Contains(err.Error(), "already in use") {
		t.Errorf("error text = %q", err.Error())
	}
}

func TestServeWritesStateFile(t *testing.T) {
	s := newTestServer(t)
	ln, err := s.Listen()
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()

	path := filepath.Join(os.Getenv("XDG_STATE_HOME"), "miodesk", "server.json")
	deadline := time.Now().Add(5 * time.Second)
	var data []byte
	for {
		data, err = os.ReadFile(path)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("state file never appeared: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	var st stateFile
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatalf("parse state: %v", err)
	}
	if st.PID != os.Getpid() || st.Port != port {
		t.Errorf("state = %+v", st)
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatalf("stat state file: %v", err)
	} else if info.Mode().Perm() != 0o600 {
		t.Errorf("state file mode = %o, want 600", info.Mode().Perm())
	}
	if info, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Fatalf("stat state dir: %v", err)
	} else if info.Mode().Perm() != 0o700 {
		t.Errorf("state dir mode = %o, want 700", info.Mode().Perm())
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not stop")
	}
}

// TestRunStdio speaks newline-delimited JSON-RPC over real pipes with the
// stdio transport, the way a local MCP client does.
func TestRunStdio(t *testing.T) {
	s := newTestServer(t)

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer stdinR.Close()
	defer stdoutR.Close()

	savedIn, savedOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = stdinR, stdoutW
	defer func() { os.Stdin, os.Stdout = savedIn, savedOut }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.RunStdio(ctx) }()

	send := func(v map[string]any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		_, err = stdinW.Write(append(b, '\n'))
		return err
	}

	if err := send(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "raw-client", "version": "0"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	reader := bufio.NewReader(stdoutR)
	resp := readJSONLine(t, reader)
	if resp["id"] != float64(1) {
		t.Fatalf("initialize response = %v", resp)
	}
	result, _ := resp["result"].(map[string]any)
	serverInfo, _ := result["serverInfo"].(map[string]any)
	if serverInfo["name"] != "miodesk" {
		t.Errorf("serverInfo = %v", serverInfo)
	}

	// notifications/initialized, then tools/list.
	if err := send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); err != nil {
		t.Fatal(err)
	}
	if err := send(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": map[string]any{},
	}); err != nil {
		t.Fatal(err)
	}
	resp = readJSONLine(t, reader)
	result, _ = resp["result"].(map[string]any)
	toolList, _ := result["tools"].([]any)
	if len(toolList) != 11 {
		t.Errorf("tools/list returned %d tools", len(toolList))
	}

	// EOF on stdin ends the session cleanly.
	stdinW.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("RunStdio: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunStdio did not return after stdin EOF")
	}
}

func readJSONLine(t *testing.T, r io.Reader) map[string]any {
	t.Helper()
	lineCh := make(chan string, 1)
	go func() {
		line, err := bufio.NewReader(r).ReadString('\n')
		if err != nil && line == "" {
			close(lineCh)
			return
		}
		lineCh <- line
	}()
	select {
	case line, ok := <-lineCh:
		if !ok {
			t.Fatal("no response from stdio server")
		}
		var msg map[string]any
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("bad response %q: %v", line, err)
		}
		return msg
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for stdio response")
		return nil
	}
}

func TestMCPNewTools(t *testing.T) {
	s := newTestServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "miodesk-test", Version: "0"}, nil)
	sess, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             ts.URL + "/mcp",
		HTTPClient:           ts.Client(),
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer sess.Close()

	callOK := func(name string, args map[string]any) map[string]any {
		t.Helper()
		call, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if call.IsError {
			t.Fatalf("%s returned tool error: %v", name, call.Content)
		}
		data, err := json.Marshal(call.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("%s structured content: %v (%s)", name, err, data)
		}
		return out
	}

	// write → edit → read back → delete
	writeOut := callOK("write", map[string]any{"path": "notes.md", "content": "hello\nworld\n"})
	if writeOut["created"] != true {
		t.Errorf("write output = %v", writeOut)
	}

	editOut := callOK("edit", map[string]any{"operations": []map[string]any{
		{"path": "notes.md", "old": "world", "new": "miodesk"},
	}})
	files, _ := editOut["files"].([]any)
	if len(files) != 1 {
		t.Fatalf("edit files = %v", editOut)
	}

	readOut := callOK("read", map[string]any{"path": "notes.md"})
	if readOut["content"] != "hello\nmiodesk\n" {
		t.Errorf("read after edit = %v", readOut)
	}

	// command runs in the workspace and reports exit codes.
	cmdOut := callOK("command", map[string]any{"command": "echo cmd-ok", "timeout": 30})
	if !strings.Contains(fmt.Sprint(cmdOut["stdout"]), "cmd-ok") {
		t.Errorf("command output = %v", cmdOut)
	}

	// command lifecycle: start → poll → cancel
	startOut := callOK("command_start", map[string]any{"command": "sleep 30", "label": "Wait for test"})
	id, _ := startOut["id"].(string)
	if id == "" || startOut["kind"] != "task" || startOut["label"] != "Wait for test" || startOut["status"] != "running" {
		t.Fatalf("command_start = %v", startOut)
	}
	pollOut := callOK("command_poll", map[string]any{"id": id})
	if pollOut["status"] != "running" && pollOut["status"] != "done" {
		t.Errorf("poll status = %v", pollOut)
	}
	if pollOut["kind"] != "task" || pollOut["label"] != "Wait for test" {
		t.Errorf("poll task identity = %v", pollOut)
	}
	cancelOut := callOK("command_cancel", map[string]any{"id": id})
	if cancelOut["status"] != "done" {
		t.Errorf("cancel status = %v", cancelOut)
	}

	delOut := callOK("delete", map[string]any{"path": "notes.md"})
	if delOut["kind"] != "delete" || delOut["target"] != "file" {
		t.Errorf("delete output = %v", delOut)
	}
	if _, err := os.Stat(filepath.Join(s.ws.Root(), "notes.md")); !os.IsNotExist(err) {
		t.Error("deleted file must be gone")
	}

	// Stats reflect the calls made above.
	status := s.Status()
	if status.Stats.Total == 0 || status.Stats.Calls["edit"] != 1 {
		t.Errorf("stats = %+v", status.Stats)
	}

	// /api/edits serves the recorded edit for the diff viewer.
	resp, err := http.Get(ts.URL + "/api/edits")
	if err != nil {
		t.Fatal(err)
	}
	var edits []EditRecord
	if err := json.NewDecoder(resp.Body).Decode(&edits); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if len(edits) != 1 || len(edits[0].Files) != 1 || len(edits[0].Files[0].Diff) == 0 {
		t.Errorf("api/edits = %+v", edits)
	}
}

func TestMCPAppsDashboard(t *testing.T) {
	s := newTestServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "miodesk-test", Version: "0"}, nil)
	sess, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             ts.URL + "/mcp",
		HTTPClient:           ts.Client(),
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer sess.Close()

	// The widget resource is readable with the MCP Apps media type, holds
	// the inlined dashboard, and carries the ui meta.
	read, err := sess.ReadResource(ctx, &mcp.ReadResourceParams{URI: adapter.WidgetURI})
	if err != nil {
		t.Fatalf("resources/read: %v", err)
	}
	if len(read.Contents) != 1 {
		t.Fatalf("contents = %d", len(read.Contents))
	}
	rc := read.Contents[0]
	if rc.MIMEType != "text/html;profile=mcp-app" {
		t.Errorf("mimeType = %q", rc.MIMEType)
	}
	if !strings.Contains(rc.Text, "<style>") || !strings.Contains(rc.Text, "MIODESK_RENDERERS") {
		t.Error("resource text should be the assembled dashboard HTML")
	}
	if !strings.Contains(rc.Text, "--color-background-primary") || !strings.Contains(rc.Text, "safeAreaInsets") {
		t.Error("widget should consume MCP Apps host style variables and safe-area context")
	}
	if ui, ok := rc.Meta["ui"].(map[string]any); !ok || ui["prefersBorder"] != true {
		t.Errorf("contents _meta.ui = %v", rc.Meta)
	}
	for _, legacyURI := range []string{
		"ui://miodesk/status.html",
		"ui://miodesk/status-v2.html",
		"ui://miodesk/status-v3.html",
		"ui://miodesk/status-v4.html",
		"ui://miodesk/status-v5.html",
		"ui://miodesk/status-v6.html",
		"ui://miodesk/status-v7.html",
	} {
		legacyRead, err := sess.ReadResource(ctx, &mcp.ReadResourceParams{URI: legacyURI})
		if err != nil {
			t.Fatalf("legacy resources/read %q: %v", legacyURI, err)
		}
		if len(legacyRead.Contents) != 1 || legacyRead.Contents[0].URI != legacyURI || legacyRead.Contents[0].Text != rc.Text {
			t.Errorf("legacy resource %q was not served as the current widget", legacyURI)
		}
	}

	taskRead, err := sess.ReadResource(ctx, &mcp.ReadResourceParams{URI: adapter.TaskWidgetURI})
	if err != nil {
		t.Fatalf("task resources/read: %v", err)
	}
	if len(taskRead.Contents) != 1 || taskRead.Contents[0].URI != adapter.TaskWidgetURI || taskRead.Contents[0].Text != rc.Text {
		t.Errorf("task resource should reuse the current self-contained widget")
	}

	// Status and command_start declare UI resources; command_poll/cancel stay
	// native so repeated lifecycle calls never create extra iframes.
	listed, err := sess.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	var status *mcp.Tool
	var commandStart *mcp.Tool
	var read2 *mcp.Tool
	for _, tl := range listed.Tools {
		switch tl.Name {
		case "status":
			status = tl
		case "command_start":
			commandStart = tl
		case "read":
			read2 = tl
		}
	}
	if status == nil || commandStart == nil || read2 == nil {
		t.Fatal("status/command_start/read tools missing")
	}
	ui, _ := status.Meta["ui"].(map[string]any)
	if ui == nil || ui["resourceUri"] != adapter.WidgetURI {
		t.Errorf("status _meta.ui = %v", status.Meta)
	}
	if status.Meta["openai/outputTemplate"] != adapter.WidgetURI {
		t.Errorf("ChatGPT alias missing: %v", status.Meta)
	}
	startUI, _ := commandStart.Meta["ui"].(map[string]any)
	if startUI == nil || startUI["resourceUri"] != adapter.TaskWidgetURI || commandStart.Meta["openai/outputTemplate"] != adapter.TaskWidgetURI {
		t.Errorf("command_start task UI metadata = %v", commandStart.Meta)
	}
	if status.Annotations == nil || !status.Annotations.ReadOnlyHint {
		t.Errorf("status annotations = %+v", status.Annotations)
	}
	if status.Title != "miodesk status" {
		t.Errorf("status title = %q", status.Title)
	}
	// Core tool annotations survive the roundtrip.
	if read2.Annotations == nil || !read2.Annotations.ReadOnlyHint {
		t.Errorf("read annotations = %+v", read2.Annotations)
	}
	if read2.Title != "Read file" {
		t.Errorf("read title = %q", read2.Title)
	}

	for _, tl := range listed.Tools {
		_, hasUI := tl.Meta["ui"]
		_, hasTemplate := tl.Meta["openai/outputTemplate"]
		wantUI := tl.Name == "status" || tl.Name == "command_start"
		if hasUI != wantUI || hasTemplate != wantUI {
			t.Errorf("%s UI metadata present = (%v, %v), want = %v; meta = %v", tl.Name, hasUI, hasTemplate, wantUI, tl.Meta)
		}
		if tl.Name == "command_start" || tl.Name == "command_poll" || tl.Name == "command_cancel" {
			if tl.Meta["openai/toolInvocation/invoking"] == nil || tl.Meta["openai/toolInvocation/invoked"] == nil {
				t.Errorf("%s should keep native invocation labels: %v", tl.Name, tl.Meta)
			}
		}
	}

	// The model-facing status tool is deliberately compact. Rich local
	// diagnostics (tool metadata and edit history) stay on the local APIs.
	call, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "status", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("call status: %v", err)
	}
	if call.IsError {
		t.Fatalf("status tool error: %v", call.Content)
	}
	data, _ := json.Marshal(call.StructuredContent)
	var dash map[string]any
	if err := json.Unmarshal(data, &dash); err != nil {
		t.Fatalf("structured content: %v (%s)", err, data)
	}
	for _, key := range []string{"kind", "workspace", "endpoint", "remote", "tunnel", "uptime_seconds", "tool_calls"} {
		if _, ok := dash[key]; !ok {
			t.Errorf("status payload missing %q: %s", key, data)
		}
	}
	for _, key := range []string{"tools", "stats", "edits", "started_at"} {
		if _, ok := dash[key]; ok {
			t.Errorf("status payload should not repeat %q into model context: %s", key, data)
		}
	}
	if len(data) > 2048 {
		t.Errorf("status payload unexpectedly large: %d bytes: %s", len(data), data)
	}

	// The output schema is declared, per the ChatGPT reference.
	if status.OutputSchema == nil {
		t.Error("status tool should declare an outputSchema")
	}
}
