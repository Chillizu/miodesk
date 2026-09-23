package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Chillizu/miodesk/internal/tools"
)

func TestLegacyCommandCompatibilityRejectsOversizedBody(t *testing.T) {
	body := strings.NewReader(strings.Repeat("x", (16<<20)+1))
	req := httptest.NewRequest(http.MethodPost, "/mcp", body)
	rec := httptest.NewRecorder()
	called := false
	h := legacyCommandCompatibility(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
	}
	if got, want := rec.Body.String(), "MCP request body exceeds the 16 MiB limit\n"; got != want {
		t.Fatalf("response body = %q, want %q", got, want)
	}
	if called {
		t.Fatal("oversized request reached the MCP handler")
	}
}

func TestLegacyCommandCompatibilityRejectsUnreadableBodyWithoutEcho(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Body = compatibilityErrorBody{}
	rec := httptest.NewRecorder()
	called := false
	legacyCommandCompatibility(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	})).ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if got, want := rec.Body.String(), "invalid MCP request body\n"; got != want {
		t.Fatalf("response body = %q, want %q", got, want)
	}
	if called {
		t.Fatal("unreadable request reached the MCP handler")
	}
}

type compatibilityErrorBody struct{}

func (compatibilityErrorBody) Read([]byte) (int, error) { return 0, errors.New("private body marker") }
func (compatibilityErrorBody) Close() error             { return nil }

func TestLegacyCommandCompatibilityLeavesNonMCPPathsAlone(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"command","arguments":{"command":"echo ok"}}}`
	req := httptest.NewRequest(http.MethodPost, "/other", strings.NewReader(body))
	req.Header.Set("Mcp-Name", "command")
	rec := httptest.NewRecorder()
	h := legacyCommandCompatibility(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := legacyToolName(r.Context()); got != "" {
			t.Fatalf("legacy tool context = %q, want empty", got)
		}
		if got := r.Header.Get("Mcp-Name"); got != "command" {
			t.Fatalf("Mcp-Name = %q, want command", got)
		}
		got, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if string(got) != body {
			t.Fatalf("body changed: %s", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func BenchmarkLegacyBodyLimit(b *testing.B) {
	body := strings.Repeat("x", (16<<20)+1)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
		rec := httptest.NewRecorder()
		legacyCommandCompatibility(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			b.Fatal("oversized request reached the MCP handler")
		})).ServeHTTP(rec, req)
		if rec.Code != http.StatusRequestEntityTooLarge {
			b.Fatalf("status = %d, want %d", rec.Code, http.StatusRequestEntityTooLarge)
		}
	}
}

func TestLegacyCommandCompatibilityRewritesMCPNameHeader(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"command","arguments":{"command":"echo ok"}}}`
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Mcp-Name"); got != "exec_command" {
			t.Fatalf("Mcp-Name = %q, want exec_command", got)
		}
		if got := legacyToolName(r.Context()); got != "command" {
			t.Fatalf("legacy tool context = %q, want command", got)
		}
		rewritten, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read rewritten body: %v", err)
		}
		name, _ := decodedToolCall(t, rewritten)
		if name != "exec_command" {
			t.Fatalf("body tool name = %q, want exec_command", name)
		}
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Mcp-Name", "command")
	rr := httptest.NewRecorder()
	legacyCommandCompatibility(next).ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusNoContent)
	}
}

func TestLegacyCommandCompatibilityUsesBodyToolName(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header string
	}{
		{name: "missing header"},
		{name: "mismatched header", header: "command"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"jsonrpc":"2.0","id":8,"method":"tools/call","params":{"name":"command_start","arguments":{"command":"sleep 1"}}}`
			req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
			if tc.header != "" {
				req.Header.Set("Mcp-Name", tc.header)
			}
			rec := httptest.NewRecorder()
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := legacyToolName(r.Context()); got != "command_start" {
					t.Errorf("legacy tool context = %q, want command_start", got)
				}
				if got := r.Header.Get("Mcp-Name"); got != "exec_command" {
					t.Errorf("Mcp-Name = %q, want exec_command", got)
				}
				rewritten, err := io.ReadAll(r.Body)
				if err != nil {
					t.Errorf("read rewritten body: %v", err)
				} else if name, _ := decodedToolCall(t, rewritten); name != "exec_command" {
					t.Errorf("body tool name = %q, want exec_command", name)
				}
				in := tools.ExecCommandInput{}
				applyLegacyExecDefaults(r.Context(), &in)
				if in.YieldTimeMS != 1 {
					t.Errorf("yield_time_ms = %d, want command_start default 1", in.YieldTimeMS)
				}
				w.WriteHeader(http.StatusNoContent)
			})
			legacyCommandCompatibility(handler).ServeHTTP(rec, req)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
			}
		})
	}
}

func TestRewriteLegacyCommandCall(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"command","arguments":{"command":"echo ok","cwd":"data/Projects/pi-amplifier","timeout":12,"yield-time_ms":999,"yield-time-ms":998,"tty":true}}}`)
	rewritten, _, ok := rewriteLegacyToolCall(body)
	if !ok {
		t.Fatal("legacy command was not rewritten")
	}

	name, args := decodedToolCall(t, rewritten)
	if name != "exec_command" {
		t.Fatalf("name = %q, want exec_command", name)
	}
	if args["cmd"] != "echo ok" || args["workdir"] != "data/Projects/pi-amplifier" {
		t.Fatalf("rewritten args = %#v", args)
	}
	if args["timeout_ms"] != float64(12_000) {
		t.Fatalf("timeout_ms = %#v", args["timeout_ms"])
	}
	for _, newOnly := range []string{"yield-time_ms", "yield-time-ms", "tty"} {
		if _, exists := args[newOnly]; exists {
			t.Fatalf("legacy shim must not inject new-only %s: %#v", newOnly, args)
		}
	}
	for _, retired := range []string{"command", "cwd", "timeout"} {
		if _, exists := args[retired]; exists {
			t.Errorf("retired arg %q leaked into %#v", retired, args)
		}
	}
}

func TestRewriteLegacyCommandStartUsesResumableExec(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":"start","method":"tools/call","params":{"name":"command_start","arguments":{"command":"sleep 30","timeout":120,"yield-time_ms":999,"tty":true}}}`)
	rewritten, _, ok := rewriteLegacyToolCall(body)
	if !ok {
		t.Fatal("legacy command_start was not rewritten")
	}

	name, args := decodedToolCall(t, rewritten)
	if name != "exec_command" || args["cmd"] != "sleep 30" {
		t.Fatalf("rewritten call = %q %#v", name, args)
	}
	if args["timeout_ms"] != float64(120_000) {
		t.Fatalf("timeout_ms = %#v", args["timeout_ms"])
	}
	for _, newOnly := range []string{"yield-time_ms", "tty"} {
		if _, exists := args[newOnly]; exists {
			t.Fatalf("legacy shim must not inject new-only %s: %#v", newOnly, args)
		}
	}
}

func TestRewriteLegacySessionCalls(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantChars string
	}{
		{
			name:  "poll",
			input: `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"command_poll","arguments":{"id":"42","yield-time_ms":999}}}`,
		},
		{
			name:      "cancel",
			input:     `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"command_cancel","arguments":{"id":42,"yield-time_ms":999}}}`,
			wantChars: "\x03",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rewritten, _, ok := rewriteLegacyToolCall([]byte(tc.input))
			if !ok {
				t.Fatalf("legacy %s was not rewritten", tc.name)
			}
			name, args := decodedToolCall(t, rewritten)
			if name != "write_stdin" {
				t.Fatalf("name = %q, want write_stdin", name)
			}
			if args["session_id"] != float64(42) {
				t.Fatalf("session_id = %#v", args["session_id"])
			}
			if _, exists := args["yield-time_ms"]; exists {
				t.Fatalf("legacy shim must not inject new-only yield-time_ms: %#v", args)
			}
			if tc.wantChars != "" && args["chars"] != tc.wantChars {
				t.Fatalf("chars = %#v, want %q", args["chars"], tc.wantChars)
			}
			if _, exists := args["id"]; exists {
				t.Fatalf("legacy id leaked into %#v", args)
			}
		})
	}
}

func TestApplyLegacyDefaultsOutOfBand(t *testing.T) {
	execCases := []struct {
		name string
		want int
	}{
		{name: "command", want: 30_000},
		{name: "command_start", want: 1},
	}
	for _, tc := range execCases {
		ctx := context.WithValue(context.Background(), legacyToolContextKey{}, tc.name)
		in := tools.ExecCommandInput{}
		applyLegacyExecDefaults(ctx, &in)
		if in.YieldTimeMS != tc.want {
			t.Fatalf("%s yield = %d, want %d", tc.name, in.YieldTimeMS, tc.want)
		}
	}

	stdinCases := []struct {
		name string
		want int
	}{
		{name: "command_poll", want: 1},
		{name: "command_cancel", want: 1_000},
	}
	for _, tc := range stdinCases {
		ctx := context.WithValue(context.Background(), legacyToolContextKey{}, tc.name)
		in := tools.WriteStdinInput{}
		applyLegacyWriteStdinDefaults(ctx, &in)
		if in.YieldTimeMS != tc.want {
			t.Fatalf("%s yield = %d, want %d", tc.name, in.YieldTimeMS, tc.want)
		}
	}
}

func TestRewriteLegacyToolCallRejectsUnknownRetiredTool(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"command_wait","arguments":{"id":42}}}`)
	rewritten, _, ok := rewriteLegacyToolCall(body)
	if ok {
		t.Fatalf("unknown retired tool unexpectedly rewritten: %s", rewritten)
	}
}

func TestRewriteLegacyToolCallLeavesUnifiedTrafficAlone(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"exec_command","arguments":{"cmd":"echo ok"}}}`)
	rewritten, _, ok := rewriteLegacyToolCall(body)
	if ok {
		t.Fatalf("unified call unexpectedly rewritten: %s", rewritten)
	}
	if string(rewritten) != string(body) {
		t.Fatalf("unified request changed: %s", rewritten)
	}
}

func decodedToolCall(t *testing.T, body []byte) (string, map[string]any) {
	t.Helper()
	var envelope struct {
		Params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		} `json:"params"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode rewritten request: %v", err)
	}
	return envelope.Params.Name, envelope.Params.Arguments
}
