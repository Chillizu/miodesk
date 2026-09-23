package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLegacyCommandCompatibilityRewritesMCPNameHeader(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"command","arguments":{"command":"echo ok"}}}`
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Mcp-Name"); got != "exec_command" {
			t.Fatalf("Mcp-Name = %q, want exec_command", got)
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

func TestRewriteLegacyCommandCall(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"command","arguments":{"command":"echo ok","cwd":"data/Projects/pi-amplifier","timeout":12}}}`)
	rewritten, ok := rewriteLegacyToolCall(body)
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
	if args["yield-time_ms"] != float64(12_000) {
		t.Fatalf("yield-time_ms = %#v, want 12000", args["yield-time_ms"])
	}
	if tty, ok := args["tty"].(bool); !ok || tty {
		t.Fatalf("tty = %#v, want false", args["tty"])
	}
	for _, retired := range []string{"command", "cwd", "timeout"} {
		if _, exists := args[retired]; exists {
			t.Errorf("retired arg %q leaked into %#v", retired, args)
		}
	}
}

func TestRewriteLegacyCommandStartUsesResumableExec(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":"start","method":"tools/call","params":{"name":"command_start","arguments":{"command":"sleep 30","timeout":120}}}`)
	rewritten, ok := rewriteLegacyToolCall(body)
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
	if args["yield-time_ms"] != float64(legacyStartCommandYieldMS) {
		t.Fatalf("yield-time_ms = %#v, want %d", args["yield-time_ms"], legacyStartCommandYieldMS)
	}
}

func TestRewriteLegacySessionCalls(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantChars string
		wantYield int
	}{
		{
			name:      "poll",
			input:     `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"command_poll","arguments":{"id":"42"}}}`,
			wantYield: legacyPollYieldMS,
		},
		{
			name:      "cancel",
			input:     `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"command_cancel","arguments":{"id":42}}}`,
			wantChars: "\x03",
			wantYield: legacyCancelYieldMS,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rewritten, ok := rewriteLegacyToolCall([]byte(tc.input))
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
			if args["yield-time_ms"] != float64(tc.wantYield) {
				t.Fatalf("yield-time_ms = %#v, want %d", args["yield-time_ms"], tc.wantYield)
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

func TestRewriteLegacyToolCallRejectsUnknownRetiredTool(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"command_wait","arguments":{"id":42}}}`)
	rewritten, ok := rewriteLegacyToolCall(body)
	if ok {
		t.Fatalf("unknown retired tool unexpectedly rewritten: %s", rewritten)
	}
}

func TestRewriteLegacyToolCallLeavesUnifiedTrafficAlone(t *testing.T) {
	body := []byte(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"exec_command","arguments":{"cmd":"echo ok"}}}`)
	rewritten, ok := rewriteLegacyToolCall(body)
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
