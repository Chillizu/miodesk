package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
)

const (
	legacyShortCommandYieldMS = 30_000
	legacyStartCommandYieldMS = 1
	legacyPollYieldMS         = 1
	legacyCancelYieldMS       = 1_000
)

// legacyCommandCompatibility is a narrow migration shim for clients that
// cached the pre-unified command schema. It rewrites only the four tools that
// were actually advertised before exec_command/write_stdin, and never exposes
// retired tools from tools/list. Remove this once stale connector schemas have
// aged out; new clients must use exec_command + write_stdin directly.
func legacyCommandCompatibility(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Body == nil {
			next.ServeHTTP(w, r)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "invalid MCP request body", http.StatusBadRequest)
			return
		}
		_ = r.Body.Close()

		if rewritten, ok := rewriteLegacyToolCall(body); ok {
			body = rewritten
			if target := legacyToolTarget(r.Header.Get("Mcp-Name")); target != "" {
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

func rewriteLegacyToolCall(body []byte) ([]byte, bool) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return body, false
	}

	var method string
	if err := json.Unmarshal(envelope["method"], &method); err != nil || method != "tools/call" {
		return body, false
	}

	var params map[string]json.RawMessage
	if err := json.Unmarshal(envelope["params"], &params); err != nil {
		return body, false
	}

	var name string
	if err := json.Unmarshal(params["name"], &name); err != nil {
		return body, false
	}

	args := map[string]json.RawMessage{}
	if raw, ok := params["arguments"]; ok && len(raw) != 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &args); err != nil {
			return body, false
		}
	}

	switch name {
	case "command":
		rewriteLegacyExecArgs(args, legacyShortCommandYieldMS)
		name = "exec_command"
	case "command_start":
		rewriteLegacyExecArgs(args, legacyStartCommandYieldMS)
		name = "exec_command"
	case "command_poll":
		rewriteLegacySessionArgs(args, "", legacyPollYieldMS)
		name = "write_stdin"
	case "command_cancel":
		rewriteLegacySessionArgs(args, "\x03", legacyCancelYieldMS)
		name = "write_stdin"
	default:
		return body, false
	}

	nameJSON, _ := json.Marshal(name)
	argsJSON, _ := json.Marshal(args)
	params["name"] = nameJSON
	params["arguments"] = argsJSON
	paramsJSON, _ := json.Marshal(params)
	envelope["params"] = paramsJSON

	rewritten, err := json.Marshal(envelope)
	if err != nil {
		return body, false
	}
	return rewritten, true
}

func rewriteLegacyExecArgs(args map[string]json.RawMessage, defaultYieldMS int) {
	moveRawArg(args, "command", "cmd")
	moveRawArg(args, "cwd", "workdir")

	timeoutMS := 0
	if raw, ok := args["timeout"]; ok {
		if seconds, err := rawInt(raw); err == nil && seconds > 0 {
			timeoutMS = seconds * 1000
			args["timeout_ms"] = rawJSONInt(timeoutMS)
		}
		delete(args, "timeout")
	}

	yieldMS := defaultYieldMS
	if timeoutMS > 0 && timeoutMS < yieldMS {
		yieldMS = timeoutMS
	}
	if _, ok := args["yield-time_ms"]; !ok {
		args["yield-time_ms"] = rawJSONInt(yieldMS)
	}
	if _, ok := args["tty"]; !ok {
		args["tty"] = json.RawMessage("false")
	}
}

func rewriteLegacySessionArgs(args map[string]json.RawMessage, chars string, yieldMS int) {
	if raw, ok := args["id"]; ok {
		if id, err := rawInt(raw); err == nil {
			args["session_id"] = rawJSONInt(id)
		} else {
			args["session_id"] = raw
		}
		delete(args, "id")
	}

	delete(args, "timeout")
	if _, ok := args["yield-time_ms"]; !ok {
		args["yield-time_ms"] = rawJSONInt(yieldMS)
	}
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
