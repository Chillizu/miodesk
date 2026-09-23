package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/Chillizu/miodesk/internal/tools"
)

const maxMCPRequestBytes int64 = 16 << 20

// legacyCommandCompatibility is a narrow migration shim for clients that
// cached the pre-unified command schema. It rewrites only the four tools that
// were actually advertised before exec_command/write_stdin, and never exposes
// retired tools from tools/list. Remove this once stale connector schemas have
// aged out; new clients must use exec_command + write_stdin directly.
func legacyCommandCompatibility(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/mcp" || r.Body == nil {
			next.ServeHTTP(w, r)
			return
		}

		defer r.Body.Close()
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxMCPRequestBytes))
		if err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) {
				http.Error(w, "MCP request body exceeds the 16 MiB limit", http.StatusRequestEntityTooLarge)
				return
			}
			http.Error(w, "invalid MCP request body", http.StatusBadRequest)
			return
		}

		if rewritten, legacyName, ok := rewriteLegacyToolCall(body); ok {
			body = rewritten
			if target := legacyToolTarget(legacyName); target != "" {
				r = r.WithContext(context.WithValue(r.Context(), legacyToolContextKey{}, legacyName))
				r.Header.Set("Mcp-Name", target)
			}
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		next.ServeHTTP(w, r)
	})
}

func legacyToolTarget(name string) string {
	switch name {
	case "command", "command_start":
		return "exec_command"
	case "command_poll", "command_cancel":
		return "write_stdin"
	default:
		return ""
	}
}

func rewriteLegacyToolCall(body []byte) (rewritten []byte, legacyName string, ok bool) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return body, "", false
	}

	var method string
	if err := json.Unmarshal(envelope["method"], &method); err != nil || method != "tools/call" {
		return body, "", false
	}

	var params map[string]json.RawMessage
	if err := json.Unmarshal(envelope["params"], &params); err != nil {
		return body, "", false
	}

	var name string
	if err := json.Unmarshal(params["name"], &name); err != nil {
		return body, "", false
	}
	legacyName = name

	args := map[string]json.RawMessage{}
	if raw, ok := params["arguments"]; ok && len(raw) != 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &args); err != nil {
			return body, "", false
		}
	}

	switch name {
	case "command", "command_start":
		rewriteLegacyExecArgs(args)
		name = "exec_command"
	case "command_poll":
		rewriteLegacySessionArgs(args, "")
		name = "write_stdin"
	case "command_cancel":
		rewriteLegacySessionArgs(args, "\x03")
		name = "write_stdin"
	default:
		return body, "", false
	}

	nameJSON, _ := json.Marshal(name)
	argsJSON, _ := json.Marshal(args)
	params["name"] = nameJSON
	params["arguments"] = argsJSON
	paramsJSON, _ := json.Marshal(params)
	envelope["params"] = paramsJSON

	rewritten, err := json.Marshal(envelope)
	if err != nil {
		return body, "", false
	}
	return rewritten, legacyName, true
}

func rewriteLegacyExecArgs(args map[string]json.RawMessage) {
	// Cached legacy connector calls can leak unified-only transport fields.
	// Strip them before unified schema validation; legacy timing semantics are
	// restored later via context-aware defaults after decoding.
	delete(args, "yield-time_ms")
	delete(args, "yield-time-ms")
	delete(args, "tty")

	moveRawArg(args, "command", "cmd")
	moveRawArg(args, "cwd", "workdir")

	if raw, ok := args["timeout"]; ok {
		if seconds, err := rawInt(raw); err == nil && seconds > 0 {
			args["timeout_ms"] = rawJSONInt(seconds * 1000)
		}
		delete(args, "timeout")
	}
}

func rewriteLegacySessionArgs(args map[string]json.RawMessage, chars string) {
	// Poll/cancel calls may receive the same leaked unified timing field.
	delete(args, "yield-time_ms")
	delete(args, "yield-time-ms")

	if raw, ok := args["id"]; ok {
		if id, err := rawInt(raw); err == nil {
			args["session_id"] = rawJSONInt(id)
		} else {
			args["session_id"] = raw
		}
		delete(args, "id")
	}

	delete(args, "timeout")
	if chars != "" {
		encoded, _ := json.Marshal(chars)
		args["chars"] = encoded
	}
}

func moveRawArg(args map[string]json.RawMessage, oldName, newName string) {
	raw, ok := args[oldName]
	if !ok {
		return
	}
	if _, exists := args[newName]; !exists {
		args[newName] = raw
	}
	delete(args, oldName)
}

func rawInt(raw json.RawMessage) (int, error) {
	var value int
	if err := json.Unmarshal(raw, &value); err == nil {
		return value, nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, err
	}
	return strconv.Atoi(text)
}

func rawJSONInt(value int) json.RawMessage {
	return json.RawMessage(strconv.Itoa(value))
}

type legacyToolContextKey struct{}

func applyLegacyExecDefaults(ctx context.Context, in *tools.ExecCommandInput) {
	if in == nil || in.YieldTimeMS != 0 {
		return
	}
	switch legacyToolName(ctx) {
	case "command":
		in.YieldTimeMS = 30_000
	case "command_start":
		in.YieldTimeMS = 1
	}
}

func applyLegacyWriteStdinDefaults(ctx context.Context, in *tools.WriteStdinInput) {
	if in == nil || in.YieldTimeMS != 0 {
		return
	}
	switch legacyToolName(ctx) {
	case "command_poll":
		in.YieldTimeMS = 1
	case "command_cancel":
		in.YieldTimeMS = 1_000
	}
}

func legacyToolName(ctx context.Context) string {
	name, _ := ctx.Value(legacyToolContextKey{}).(string)
	return name
}
