# miodesk HTTP and Exec Bounds Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bound MCP request acquisition and stdin writes, and make legacy command normalization independent of the optional `Mcp-Name` header.

**Architecture:** Keep the official MCP Go SDK as the protocol handler. The server applies HTTP limits before the temporary legacy adapter; the adapter derives legacy identity from the request body and changes only retired calls; unified exec handlers enforce input and write-time limits before returning to the existing session lifecycle.

**Tech Stack:** Go standard library, official MCP Go SDK v1.7.0, `httptest`, existing exec session manager.

**Spec:** `docs/superpowers/specs/2026-09-23-miodesk-maintenance-design.md`

## Global Constraints

- Keep the MCP POST body cap at 16 MiB and full-request read deadline at 30 seconds.
- Keep each `write_stdin.chars` input at or below 64 KiB and each blocking write at or below 5 seconds.
- Retired `command`, `command_start`, `command_poll`, and `command_cancel` remain absent from `tools/list`.
- Body `params.name` is authoritative; `Mcp-Name` is a mirror used for transport compatibility.
- The compatibility layer strips observed unified-only leaked fields and never adds undeclared legacy behavior.
- Do not log request bodies, command stdin, file contents, or authorization headers.
- Use only the Go standard library and the existing MCP SDK dependency.

## Review Focus

- A POST body one byte above 16 MiB must stop at the cap and return 413 without reaching the MCP SDK.
- A slow body reader must time out within the configured read deadline without constraining later tool execution time.
- `command_start` with no `Mcp-Name` header must still receive the legacy timing default.
- A mismatched `Mcp-Name` header must not override the body tool name.
- A child that never reads stdin must not hold `write_stdin` beyond 5 seconds or wedge later session operations.

## File Structure

- `internal/server/legacy_exec_compat.go`: bounded body read for `POST /mcp` and retired-tool normalization only; other paths and methods pass through unchanged.
- `internal/server/server.go`: shared HTTP server constructor and request read timeout.
- `internal/tools/unified_exec.go`: input-size validation before session writes.
- `internal/tools/process.go`: serialized, interruptible write and one-time stdin close.
- Existing test files own request behavior and command-session behavior; no new package or dependency is introduced.

---

### Task 1: Bound MCP HTTP request bodies and acquisition time

**Files:**
- Modify: `internal/server/legacy_exec_compat.go`
- Modify: `internal/server/server.go`
- Test: `internal/server/legacy_exec_compat_test.go`
- Test: `internal/server/server_test.go`

**Interfaces:**
- Add `maxMCPRequestBytes int64 = 16 << 20` and `mcpRequestReadTimeout = 30 * time.Second` in `internal/server`.
- Add `newHTTPServer(handler http.Handler, readTimeout time.Duration) *http.Server`; `Serve` must call it with `mcpRequestReadTimeout`.
- Keep `legacyCommandCompatibility(next http.Handler) http.Handler` as the middleware entry point.

- [x] **Step 1: Add the oversized-body test**

```go
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
	if called {
		t.Fatal("oversized request reached the MCP handler")
	}
}
```

- [x] **Step 2: Run the test and confirm the current middleware fails it**

Run: `go test ./internal/server -run '^TestLegacyCommandCompatibilityRejectsOversizedBody$' -count=1`

Expected: FAIL because the current middleware reads and forwards the complete body.

- [x] **Step 3: Implement the request-body cap**

Only for `POST /mcp`, wrap `r.Body` with `http.MaxBytesReader(w, r.Body, maxMCPRequestBytes)` before `io.ReadAll`. If `errors.As(err, &maxBytesErr)` matches `*http.MaxBytesError`, return HTTP 413 with the fixed message `MCP request body exceeds the 16 MiB limit`. For other read failures, return HTTP 400 with the fixed message `invalid MCP request body`. Never include body bytes in the response. Preserve pass-through behavior for every other method and path.

- [x] **Step 4: Extract and test the server read timeout**

Implement:

```go
func newHTTPServer(handler http.Handler, readTimeout time.Duration) *http.Server {
	return &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       readTimeout,
	}
}
```

Replace the inline `http.Server` literal in `Serve` with `newHTTPServer(s.Handler(), mcpRequestReadTimeout)`. In `server_test.go`, start an `httptest.NewUnstartedServer`, set its `Config` with `newHTTPServer`, use a 25 ms timeout, and post from an `io.Pipe` whose writer pauses after the first byte. Assert the client returns within one second and the handler does not process a complete MCP call.

Also add `TestServeHTTPReadTimeoutDoesNotLimitHandler`: send a complete POST body through a server with a 25 ms read timeout, let its handler wait 50 ms after the body has been read, and assert the response still succeeds. Add `TestServeHTTPRejectsMalformedMCPJSON`: post a fixed malformed body to the real handler and assert HTTP 400 with a fixed response that does not echo any request bytes.

- [x] **Step 5: Add a request-cap benchmark**

Add `BenchmarkLegacyBodyLimit` in `legacy_exec_compat_test.go`; reuse one `(16<<20)+1` byte body value, create a new reader/request/recorder each iteration, run the middleware, and call `b.ReportAllocs()` plus `b.SetBytes(int64(len(body)))`. Assert each response is 413 so the benchmark cannot silently measure a different path.

- [x] **Step 6: Re-run focused tests and commit**

Run: `go test ./internal/server -run 'TestLegacyCommandCompatibilityRejectsOversizedBody|TestServeHTTPReadTimeout|TestServeHTTPReadTimeoutDoesNotLimitHandler|TestServeHTTPRejectsMalformedMCPJSON' -count=1`

Expected: PASS. Then run `git diff --check` and commit the four changed paths with message `fix: bound MCP request acquisition`.

### Task 2: Use the MCP body as the legacy tool identity source

**Files:**
- Modify: `internal/server/legacy_exec_compat.go`
- Test: `internal/server/legacy_exec_compat_test.go`

**Interfaces:**
- Change the helper to `rewriteLegacyToolCall(body []byte) (rewritten []byte, legacyName string, ok bool)`.
- Preserve `legacyToolTarget(name string) string` and `legacyToolContextKey`.

- [x] **Step 1: Add missing-header and mismatched-header tests**

Add `TestLegacyCommandCompatibilityUsesBodyToolName`. Send an MCP body whose `params.name` is `command_start` first without `Mcp-Name`, then with `Mcp-Name: command`. In both cases assert the rewritten body names `exec_command`, the request context records `command_start`, and `applyLegacyExecDefaults` applies `command_start` defaults. Extend `TestRewriteLegacyCommandCall` to include both leaked fields `yield-time_ms` and `yield-time-ms` plus `tty`, and assert all three are removed before unified schema validation.

- [x] **Step 2: Run the new tests and confirm header-dependent behavior fails**

Run: `go test ./internal/server -run '^TestLegacyCommandCompatibilityUsesBodyToolName$' -count=1`

Expected: FAIL for the missing-header and mismatched-header cases.

- [x] **Step 3: Return the parsed legacy name and use it for compatibility context**

Have `rewriteLegacyToolCall` return the original decoded name on successful rewrite. In `legacyCommandCompatibility`, use that returned name to set `legacyToolContextKey` and derive the target header. Treat the received header only as a value to synchronize after rewrite; it must not determine defaults. Preserve the current mapping of the four retired names and current unified-only-field removal.

- [x] **Step 4: Run legacy and unified compatibility tests, then commit**

Run: `go test ./internal/server -run 'TestLegacy|TestRewriteLegacy|TestApplyLegacyDefaults|TestMCPNewTools' -count=1`

Expected: PASS, with old names still absent from `tools/list` and unified requests unchanged. Run `git diff --check`; commit with message `fix: derive legacy exec identity from MCP body`.

### Task 3: Bound `write_stdin` input and blocking writes

**Files:**
- Modify: `internal/tools/unified_exec.go`
- Modify: `internal/tools/process.go`
- Test: `internal/tools/command_test.go`

**Interfaces:**
- Add `maxWriteStdinBytes = 64 << 10` and `writeStdinWriteTimeout = 5 * time.Second`.
- Add `procHandle.writeStdinBounded(ctx context.Context, chars string, timeout time.Duration) error` and `procHandle.closeStdin() error`; `WriteStdin` calls the bounded writer before its existing yield wait.
- Add a one-time close guard and closed-state flag so timeout/cancellation can close the underlying writer without waiting on the write serialization mutex.
- Preserve the `Manager.Cancel` and numeric session ID API.

- [x] **Step 1: Test oversized input rejection**

Add `TestWriteStdinRejectsOversizedInput`: start a resumable session, pass `strings.Repeat("x", (64<<10)+1)` to `WriteStdin`, assert a clear cap error, then poll the session to prove the session was not modified or wedged.

- [x] **Step 2: Run the test and confirm the current handler accepts the input**

Run: `go test ./internal/tools -run '^TestWriteStdinRejectsOversizedInput$' -count=1`

Expected: FAIL because there is no input-size check.

- [x] **Step 3: Implement the byte cap before any pipe write**

At the start of `WriteStdin`, reject `len(in.Chars) > maxWriteStdinBytes` before looking up or mutating process input. Keep empty polling and the special Ctrl-C cancellation path available.

- [x] **Step 4: Test a blocked writer and cancellation**

Add a `blockingWriteCloser` test double in `command_test.go`; its `Write` waits on a channel and its `Close` releases that channel. Inject it into a live `procHandle`, call the bounded writer with a short test timeout, and assert it returns by the timeout, closes the writer, exits the one in-flight write goroutine, and allows a subsequent write attempt to return a closed-input error without waiting on `stdinMu`.

- [x] **Step 5: Implement bounded, interruptible serialization**

Allow only one in-flight write per session: check the atomic closed flag, acquire the serialization mutex with `TryLock`, and return a clear busy error immediately if another write is active. Apply one deadline across the write. On request cancellation or the 5-second write deadline, call `closeStdin` without waiting on the write mutex, then wait up to 100 ms for the writer goroutine to exit; if it remains blocked, cancel that child process and wait once more for the process pipe to close. Implement `closeStdin` with `sync.Once` and an atomic closed flag; it closes the immutable writer pointer without mutating that pointer. Do not leave a continuing write queue or a goroutine per retry; the existing maximum of 32 live sessions bounds any one in-flight writer per session.

- [x] **Step 6: Run the command lifecycle tests and commit**

Run: `go test -race ./internal/tools -run 'TestWriteStdin|TestUnifiedExecSessionInputAndCancel|TestManagerCapsConcurrentSessions' -count=1`

Expected: PASS with timeout, cancel, poll, and continued session lifecycle behavior intact. Commit with message `fix: bound exec session stdin writes`.

### Task 4: Verify the HTTP/exec workstream

Run: `go test -race ./internal/server ./internal/tools`

Expected: PASS. Confirm `git status --short` contains only intentional changes from this workstream and that `tools/list` does not advertise the retired command quartet.
