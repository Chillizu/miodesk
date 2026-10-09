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
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Chillizu/miodesk/internal/config"
	"github.com/Chillizu/miodesk/internal/logging"
	"github.com/Chillizu/miodesk/internal/tools"
	"github.com/Chillizu/miodesk/internal/workspace"
)

func newTestServer(t *testing.T) *Server {
	t.Helper()
	// Keep persistent and runtime state inside the test sandbox.
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
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

func TestHandlerRemovesWidgetRoutesAndKeepsJSONAPIs(t *testing.T) {
	ts := httptest.NewServer(newTestServer(t).Handler())
	defer ts.Close()
	for _, path := range []string{"/", "/widget", "/preview"} {
		resp, err := ts.Client().Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", path, resp.StatusCode)
		}
	}
	for _, path := range []string{"/healthz", "/api/status", "/api/edits"} {
		resp, err := ts.Client().Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, resp.StatusCode)
		}
		if path == "/api/status" {
			var got struct {
				Kind  string `json:"kind"`
				Theme string `json:"theme"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
				t.Fatal(err)
			}
			if got.Kind != "status" || got.Theme != "auto" {
				t.Errorf("status = %+v, want kind=status theme=auto", got)
			}
		}
		resp.Body.Close()
	}
}

func TestToolsReturnsSnapshot(t *testing.T) {
	s := newTestServer(t)
	first := s.Tools()
	if len(first) == 0 {
		t.Fatal("expected registered tools")
	}
	original := first[0].Name
	first[0].Name = "mutated"
	second := s.Tools()
	if second[0].Name != original {
		t.Fatalf("Tools exposed internal slice: got %q, want %q", second[0].Name, original)
	}
}

func TestServerToolListMatchesMetadata(t *testing.T) {
	s := newTestServer(t)
	metadata := s.Tools()
	if len(metadata) != 10 {
		t.Fatalf("server tool metadata count = %d, want 10", len(metadata))
	}
	want := make(map[string]bool, len(metadata))
	for _, tool := range metadata {
		want[tool.Name] = true
	}

	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "miodesk-tool-list-test", Version: "test"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             ts.URL + "/mcp",
		HTTPClient:           ts.Client(),
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	listed, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	if len(listed.Tools) != len(metadata) {
		t.Fatalf("tools/list count = %d, server metadata count = %d", len(listed.Tools), len(metadata))
	}
	got := make(map[string]bool, len(listed.Tools))
	for _, tool := range listed.Tools {
		got[tool.Name] = true
	}
	if !got["status"] {
		t.Fatal("tools/list is missing status")
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tools/list names = %v, server metadata names = %v", got, want)
	}
}

func TestRecentEditHistoryEvictsOldestAndStaysWithinByteBudget(t *testing.T) {
	s := newTestServer(t)
	for i := 0; i < 11; i++ {
		s.recordEdits(testEditFilesWithDiff(fmt.Sprintf("edit-%02d", i)))
	}
	records := s.recentEdits()
	if len(records) != maxRecentEdits {
		t.Fatalf("recent edit records = %d, want %d", len(records), maxRecentEdits)
	}
	if got := records[0].Files[0].Diff[0].Lines[0].Text; got != "edit-10" {
		t.Errorf("newest record text = %q, want edit-10", got)
	}
	if got := records[len(records)-1].Files[0].Diff[0].Lines[0].Text; got != "edit-01" {
		t.Errorf("oldest retained record text = %q, want edit-01", got)
	}

	largeText := strings.Repeat("x", 768<<10)
	for i := 0; i < 3; i++ {
		s.recordEdits(testEditFilesWithDiff(largeText))
	}
	largeRecords := s.recentEdits()
	if len(largeRecords) != 2 {
		t.Fatalf("records after byte eviction = %d, want two 768 KiB records", len(largeRecords))
	}
	if got := recentEditDiffBytes(largeRecords); got > maxRecentEditBytes {
		t.Fatalf("retained recent diff bytes = %d, cap %d", got, maxRecentEditBytes)
	}
	if got := recentEditDiffBytes(largeRecords); got != s.editBytes {
		t.Errorf("tracked edit bytes = %d, measured retained bytes = %d", s.editBytes, got)
	}
}

func TestRecordEditsDoesNotMutateToolResultWhenTruncatingCache(t *testing.T) {
	s := newTestServer(t)
	files := testEditFilesWithDiff(strings.Repeat("large", (maxRecentEditBytes/5)+1))
	s.recordEdits(files)
	if len(files) != 1 || len(files[0].Diff) == 0 || files[0].DiffTruncated {
		t.Fatal("recordEdits mutated the tool result while truncating its cache copy")
	}

	records := s.recentEdits()
	if len(records) != 1 || len(records[0].Files) != 1 {
		t.Fatalf("recent edits = %+v", records)
	}
	cached := records[0].Files[0]
	if cached.Path != files[0].Path || cached.Bytes != files[0].Bytes || cached.Lines != files[0].Lines {
		t.Errorf("cached summary = %+v, want path/byte/line summary from %+v", cached, files[0])
	}
	if cached.Diff != nil || !cached.DiffTruncated {
		t.Errorf("cached oversized diff = %+v, want nil diff and truncated flag", cached)
	}
}

func BenchmarkRecordEditsBounded(b *testing.B) {
	files := benchmarkEditFilesNearDiffCap(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s := &Server{}
		s.recordEdits(files)
		if s.editBytes > maxRecentEditBytes {
			b.Fatalf("retained edit bytes = %d, cap %d", s.editBytes, maxRecentEditBytes)
		}
	}
}

func benchmarkEditFilesNearDiffCap(b *testing.B) []tools.EditFileResult {
	b.Helper()
	oldLines := make([]string, 16)
	newLines := make([]string, 16)
	for i := range oldLines {
		oldLines[i] = "old"
		newLines[i] = oldLines[i]
	}
	largeLine := strings.Repeat("<", 80_000)
	newLines[1] = largeLine
	newLines[14] = largeLine
	oldText := strings.Join(oldLines, "\n") + "\n"
	newText := strings.Join(newLines, "\n") + "\n"
	groups, truncated := tools.DiffLinesBounded(oldText, newText, 20_000, 1<<20)
	if truncated {
		b.Fatal("benchmark diff unexpectedly truncated")
	}
	encoded, err := json.Marshal(groups)
	if err != nil {
		b.Fatal(err)
	}
	if len(encoded) < 900_000 || len(encoded) > 1<<20 {
		b.Fatalf("benchmark diff JSON size = %d; want 900000..%d bytes", len(encoded), 1<<20)
	}
	return []tools.EditFileResult{{Path: "large.txt", Bytes: len(newText), Lines: len(newLines), Diff: groups}}
}

func testEditFilesWithDiff(text string) []tools.EditFileResult {
	return []tools.EditFileResult{{
		Path:  "file.txt",
		Bytes: len(text),
		Lines: 1,
		Diff: []tools.DiffGroup{{
			Header: "@@ -1,0 +1,1 @@",
			Lines:  []tools.DiffLine{{Type: "add", New: 1, Text: text}},
		}},
	}}
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

func TestServeHTTPReadTimeout(t *testing.T) {
	processed := make(chan struct{}, 1)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			http.Error(w, "incomplete request", http.StatusBadRequest)
			return
		}
		processed <- struct{}{}
		w.WriteHeader(http.StatusNoContent)
	})
	ts := httptest.NewUnstartedServer(handler)
	ts.Config = newHTTPServer(handler, 25*time.Millisecond)
	ts.Start()
	defer ts.Close()

	reader, writer := io.Pipe()
	defer writer.CloseWithError(io.ErrClosedPipe)
	go func() {
		_, _ = writer.Write([]byte("first"))
		time.Sleep(100 * time.Millisecond)
		_, _ = writer.Write([]byte("rest"))
		_ = writer.Close()
	}()
	request, err := http.NewRequest(http.MethodPost, ts.URL+"/mcp", reader)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	response, err := (&http.Client{Timeout: time.Second}).Do(request)
	if response != nil {
		defer response.Body.Close()
		if err == nil && response.StatusCode == http.StatusNoContent {
			t.Fatal("handler completed an incomplete request")
		}
	} else if err == nil {
		t.Fatal("slow request returned neither a response nor an error")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("slow request took %s, want at most 1s", elapsed)
	}
	select {
	case <-processed:
		t.Fatal("handler processed an incomplete MCP request")
	default:
	}
}

func TestServeHTTPReadTimeoutDoesNotLimitHandler(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Errorf("read complete request: %v", err)
			return
		}
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusNoContent)
	})
	ts := httptest.NewUnstartedServer(handler)
	ts.Config = newHTTPServer(handler, 25*time.Millisecond)
	ts.Start()
	defer ts.Close()

	response, err := ts.Client().Post(ts.URL+"/mcp", "application/json", strings.NewReader("complete body"))
	if err != nil {
		t.Fatalf("complete request failed after handler delay: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusNoContent)
	}
}

func TestServeHTTPRejectsMalformedMCPJSON(t *testing.T) {
	s := newTestServer(t)
	tsrv := httptest.NewServer(s.Handler())
	defer tsrv.Close()

	const marker = "malformed-private-payload"
	response, err := tsrv.Client().Post(tsrv.URL+"/mcp", "application/json", strings.NewReader("{\"private\":\""+marker))
	if err != nil {
		t.Fatalf("post malformed body: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusBadRequest)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read error response: %v", err)
	}
	if bytes.Contains(body, []byte(marker)) {
		t.Fatalf("malformed request body was echoed: %q", body)
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
		t.Fatalf("root: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("root = %d, want 404", resp.StatusCode)
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
	if status.Kind != "status" || status.Name != "miodesk" || status.Port != port || len(status.Tools) != 10 {
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
	if len(toolList) != 10 {
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
	initialize := sess.InitializeResult()
	if initialize == nil || initialize.ServerInfo == nil {
		t.Fatal("initialize result must include server description")
	}
	implementationDescription := strings.ToLower(initialize.ServerInfo.Description)
	if !strings.Contains(implementationDescription, "file tools") || !strings.Contains(implementationDescription, "current user's permissions") {
		t.Errorf("implementation description does not distinguish file and command boundaries: %q", initialize.ServerInfo.Description)
	}
	instructions := strings.ToLower(initialize.Instructions)
	for _, want := range []string{"file tool", "sandboxed inside the workspace root", "current user's permissions", "workspace-contained working directory", "does not restrict os filesystem access"} {
		if !strings.Contains(instructions, want) {
			t.Errorf("server instructions missing security wording %q: %q", want, initialize.Instructions)
		}
	}
	listed, err := sess.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	var execDescription string
	for _, tool := range listed.Tools {
		if tool.Name == "exec_command" {
			execDescription = strings.ToLower(tool.Description)
			break
		}
	}
	for _, want := range []string{"current user's permissions", "workspace-contained working directory", "not confined by an os filesystem sandbox"} {
		if !strings.Contains(execDescription, want) {
			t.Errorf("exec_command description missing security wording %q: %q", want, execDescription)
		}
	}

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

	// Unified exec returns final output for short commands.
	cmdOut := callOK("exec_command", map[string]any{"cmd": "echo cmd-ok", "yield_time_ms": 1000})
	if !strings.Contains(fmt.Sprint(cmdOut["output"]), "cmd-ok") || cmdOut["exit_code"] != float64(0) {
		t.Errorf("exec_command output = %v", cmdOut)
	}

	// Long pipe-backed commands yield one numeric session; write_stdin polls and Ctrl-C cancels it.
	startOut := callOK("exec_command", map[string]any{"cmd": "echo started; sleep 30", "yield_time_ms": 10})
	sessionID, _ := startOut["session_id"].(float64)
	if sessionID <= 0 {
		t.Fatalf("exec_command session = %v", startOut)
	}
	pollOut := callOK("write_stdin", map[string]any{"session_id": sessionID, "chars": "", "yield_time_ms": 10})
	if _, ok := pollOut["session_id"]; !ok {
		t.Errorf("write_stdin should keep a live session: %v", pollOut)
	}
	cancelOut := callOK("write_stdin", map[string]any{"session_id": sessionID, "chars": "\u0003", "yield_time_ms": 10})
	if _, ok := cancelOut["session_id"]; ok {
		t.Errorf("cancelled session should not remain resumable: %v", cancelOut)
	}
	if _, ok := cancelOut["exit_code"]; !ok {
		t.Errorf("cancelled session should expose an exit code: %v", cancelOut)
	}

	// context lifecycle: rolling update → checkpoint → resume → discovery list.
	if err := os.Mkdir(filepath.Join(s.ws.Root(), "project"), 0o755); err != nil {
		t.Fatal(err)
	}
	contextOut := callOK("context", map[string]any{
		"action":            "update",
		"id":                "miodesk-maintenance",
		"working_directory": "project",
		"goal":              "keep work continuous across chats",
		"current_state":     "rolling handoff is wired",
		"decisions":         []string{"working_directory is a locator, not the workspace root"},
		"next_steps":        []string{"checkpoint the milestone"},
	})
	if contextOut["kind"] != "context" || contextOut["action"] != "update" {
		t.Fatalf("context update = %v", contextOut)
	}
	active, _ := contextOut["context"].(map[string]any)
	if active["id"] != "miodesk-maintenance" || active["revision"] != float64(1) {
		t.Fatalf("active context = %v", active)
	}

	checkpointOut := callOK("context", map[string]any{
		"action": "checkpoint",
		"id":     "miodesk-maintenance",
		"label":  "Context v1 wired",
	})
	checkpoint, _ := checkpointOut["checkpoint"].(map[string]any)
	if checkpoint["id"] != "checkpoint-001" || checkpoint["label"] != "Context v1 wired" {
		t.Fatalf("context checkpoint = %v", checkpointOut)
	}

	resumeOut := callOK("context", map[string]any{"action": "resume", "id": "miodesk-maintenance"})
	resumed, _ := resumeOut["context"].(map[string]any)
	if resumed["id"] != "miodesk-maintenance" {
		t.Fatalf("context resume = %v", resumeOut)
	}
	listOut := callOK("context", map[string]any{"action": "list"})
	contexts, _ := listOut["contexts"].([]any)
	if len(contexts) != 1 {
		t.Fatalf("context list = %v", listOut)
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

	// /api/edits serves the recorded edit through the JSON API.
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

func TestContextAcceptsLegacyActiveReasoningInput(t *testing.T) {
	s := newTestServer(t)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "miodesk-context-alias-test", Version: "test"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             ts.URL + "/mcp",
		HTTPClient:           ts.Client(),
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	listed, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	var contextTool *mcp.Tool
	for _, tool := range listed.Tools {
		if tool.Name == "context" {
			contextTool = tool
			break
		}
	}
	if contextTool == nil {
		t.Fatal("context tool missing")
	}
	schema, ok := contextTool.InputSchema.(map[string]any)
	if !ok {
		t.Fatalf("context input schema = %#v", contextTool.InputSchema)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("context schema properties = %#v", schema["properties"])
	}
	if _, ok := properties["approach_summary"]; !ok {
		t.Errorf("context schema is missing approach_summary: %v", properties)
	}
	if _, ok := properties["clear_fields"]; !ok {
		t.Errorf("context schema is missing clear_fields: %v", properties)
	}
	if _, ok := properties["active_reasoning"]; ok {
		t.Errorf("context schema must not advertise active_reasoning: %v", properties)
	}

	call, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "context", Arguments: map[string]any{
		"action":           "update",
		"id":               "legacy-approach",
		"summary":          "Legacy client request",
		"active_reasoning": "concise replay summary",
	}})
	if err != nil {
		t.Fatalf("call context with legacy field: %v", err)
	}
	if call.IsError {
		t.Fatalf("context legacy alias returned error: %v", call.Content)
	}
	data, err := json.Marshal(call.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("decode context response: %v (%s)", err, data)
	}
	active, _ := payload["context"].(map[string]any)
	state, _ := active["state"].(map[string]any)
	if state["approach_summary"] != "concise replay summary" {
		t.Fatalf("context state = %v", state)
	}
}

func TestMCPStatusWithoutAppsUI(t *testing.T) {
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

	resources, err := sess.ListResources(ctx, &mcp.ListResourcesParams{})
	if err != nil {
		t.Fatalf("resources/list: %v", err)
	}
	if len(resources.Resources) != 0 {
		t.Fatalf("resources/list = %+v, want empty", resources.Resources)
	}

	listed, err := sess.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	var status *mcp.Tool
	var execCommand *mcp.Tool
	var writeStdin *mcp.Tool
	var read2 *mcp.Tool
	for _, tl := range listed.Tools {
		switch tl.Name {
		case "status":
			status = tl
		case "exec_command":
			execCommand = tl
		case "write_stdin":
			writeStdin = tl
		case "read":
			read2 = tl
		}
	}
	if status == nil || execCommand == nil || writeStdin == nil || read2 == nil {
		t.Fatal("status/unified exec/read tools missing")
	}
	for _, legacy := range []string{"command", "command_start", "command_poll", "command_wait", "command_cancel"} {
		for _, tl := range listed.Tools {
			if tl.Name == legacy {
				t.Errorf("legacy command tool %q should not be model-facing", legacy)
			}
		}
	}
	assertSchemaProperties := func(tl *mcp.Tool, want []string, reject []string) {
		t.Helper()
		if tl.OutputSchema == nil {
			t.Errorf("%s should declare an inferred outputSchema", tl.Name)
		}
		schema, ok := tl.InputSchema.(map[string]any)
		if !ok {
			t.Errorf("%s inputSchema = %#v", tl.Name, tl.InputSchema)
			return
		}
		props, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Errorf("%s properties = %#v", tl.Name, schema["properties"])
			return
		}
		for _, key := range want {
			if _, ok := props[key]; !ok {
				t.Errorf("%s schema missing %q: %#v", tl.Name, key, props)
			}
		}
		for _, key := range reject {
			if _, ok := props[key]; ok {
				t.Errorf("%s schema should not expose legacy field %q: %#v", tl.Name, key, props)
			}
		}
	}
	assertSchemaProperties(execCommand, []string{"cmd", "workdir", "tty", "yield_time_ms", "timeout_ms"}, []string{"command", "label", "id"})
	assertSchemaProperties(writeStdin, []string{"session_id", "chars", "yield_time_ms"}, []string{"id", "command"})

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

	if len(listed.Tools) != 10 {
		t.Fatalf("tools/list count = %d, want 10", len(listed.Tools))
	}
	for _, tl := range listed.Tools {
		if _, ok := tl.Meta["ui"]; ok {
			t.Errorf("%s advertises _meta.ui: %v", tl.Name, tl.Meta)
		}
		if _, ok := tl.Meta["openai/outputTemplate"]; ok {
			t.Errorf("%s advertises output template: %v", tl.Name, tl.Meta)
		}
		if tl.Name == "exec_command" || tl.Name == "write_stdin" || tl.Name == "status" {
			if tl.Meta["openai/toolInvocation/invoking"] == nil || tl.Meta["openai/toolInvocation/invoked"] == nil {
				t.Errorf("%s lost invocation labels: %v", tl.Name, tl.Meta)
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
