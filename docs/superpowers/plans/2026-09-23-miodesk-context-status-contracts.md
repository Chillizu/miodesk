# miodesk Context and Status Contracts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep the text status tool available without widget assets, make model-facing security wording accurate, and migrate context state to explicit clearing and compact approach summaries.

**Architecture:** Keep MCP tools host-agnostic and ChatGPT metadata in `internal/adapter`. Register status regardless of widget assembly success and attach UI metadata/resources only when the HTML is ready. Evolve context JSON with read compatibility for `active_reasoning`, expose `approach_summary` in the new schema, and apply validated explicit clears before persistence.

**Tech Stack:** Go standard library, official MCP Go SDK v1.7.0, the already-required `github.com/google/jsonschema-go` package for the context schema override, existing adapter/widget and XDG context store.

**Spec:** `docs/superpowers/specs/2026-09-23-miodesk-maintenance-design.md`

## Global Constraints

- All names remain lowercase `miodesk`.
- Only the diagnostics status tool may carry widget UI metadata; command and file tools remain Native-first.
- Widget failures degrade only the UI resource; status remains registered and counted.
- Command `cwd` is workspace-contained, but commands retain current-user OS permissions and are not filesystem-sandboxed.
- `approach_summary` stores only a concise current approach/rationale, never a transcript or full reasoning trace.
- Old persisted `active_reasoning` and old input JSON remain readable during migration.
- `clear_fields` accepts only the five fields named in the approved spec; unknown or duplicate entries fail before saving, and a listed field is cleared even if its value is also supplied in the request.

## Review Focus

- Missing or malformed embedded widget assets must leave a callable text status tool with no dangling UI resource URI.
- `tools/list` metadata, `Server.tools`, and status counts must agree after widget failure.
- Existing XDG context JSON containing `active_reasoning` must resume with the same content and write new state as `approach_summary` after update.
- If one scalar field is both set and listed in `clear_fields`, the clear takes precedence and the persisted revision records the cleared value.
- Clearing `working_directory` must clear only the locator and leave the workspace root unchanged.

---

### Task 1: Decouple status registration from widget assembly

**Files:**
- Modify: `internal/adapter/adapter.go`
- Test: `internal/adapter/adapter_test.go`
- Modify: `internal/server/server_test.go`

**Interfaces:**
- Keep `Attach(s *mcp.Server, assets fs.FS, data Data) error`.
- Change the status tool constructor to `dashboardTool(widgetAvailable bool) *mcp.Tool`.
- Add `StatusToolMeta(widgetAvailable bool) map[string]any`; with `false`, it returns invocation labels without UI/output-template metadata.

- [x] **Step 1: Add missing-assets list and call tests**

In `adapter_test.go`, create an MCP server, call `Attach` with `fstest.MapFS{}` and a deterministic status handler, assert `Attach` reports the widget assembly error, then connect an SDK client and call `tools/list`. Assert the `status` tool exists, has no `ui.resourceUri` or `openai/outputTemplate`, and is the sole registered tool. Call `status` and assert the deterministic handler payload is returned. In `server_test.go`, assert the normal server's `Tools()` list and MCP `tools/list` both contain the same 10 tool names, including status.

- [x] **Step 2: Run the new test and confirm status is missing**

Run: `go test ./internal/adapter -run '^TestAttachRegistersTextStatusWhenWidgetAssemblyFails$' -count=1`

Expected: FAIL because `Attach` currently returns before registering status.

- [x] **Step 3: Register status with metadata matching widget availability**

Attempt `WidgetHTML` assembly, then always add the status tool using `dashboardTool(err == nil)`. If assembly failed, register no resource, omit UI/output-template metadata, and return the wrapped error after adding the callable status handler; `server.New` already logs this error and continues serving. If assembly succeeded, register the current URI and legacy URI aliases as today. Keep invocation labels in both cases, and route all status widget metadata through `StatusToolMeta(widgetAvailable)`; `ToolMeta("status")` must not unconditionally attach a widget URI.

- [x] **Step 4: Verify server counts and commit**

Run: `go test ./internal/adapter ./internal/server -run 'TestAttachRegistersTextStatusWhenWidgetAssemblyFails|TestMCPNewTools|TestMCPAppsDashboard|TestServerToolListMatchesMetadata' -count=1`

Expected: PASS; status is counted and callable, and `tools/list` has no dangling UI resource when assets fail. Commit with message `fix: keep status available without widget assets`.

### Task 2: Migrate context summary and add explicit scalar clearing

**Files:**
- Modify: `internal/contextstore/store.go`
- Test: `internal/contextstore/store_test.go`
- Modify: `internal/server/server.go`
- Test: `internal/server/server_test.go`
- Modify: `go.mod`

**Interfaces:**
- Add `ApproachSummary string` with JSON key `approach_summary` to stored `State` and model-facing `Input`.
- Add `ClearFields []string` with JSON key `clear_fields` to `Input`.
- Keep old persisted `active_reasoning` and old context request JSON as read aliases only; `Input` and stored `State` expose only `ApproachSummary` with JSON key `approach_summary`.
- Add `clear_fields` validation to `applyPatch`; its allowlist is `working_directory`, `summary`, `goal`, `current_state`, and `approach_summary`.
- Clear semantics apply only to `update` and `checkpoint`; reject `clear_fields` for `resume` and `list`.

- [x] **Step 1: Add migration and clearing tests**

Add `TestResumeMigratesLegacyActiveReasoning`: write a fixture containing `"active_reasoning":"use the replay harness"`, resume it, assert `ApproachSummary` contains the text, update it, read the JSON file, and assert only `approach_summary` is written.

Add `TestInputUnmarshalMigratesLegacyActiveReasoning`: decode an old request containing only `active_reasoning` and assert it populates `ApproachSummary`; decode a request containing both keys and assert the present `approach_summary` value wins. Add `TestInputUnmarshalRejectsUnknownFields` and assert an unrelated string property returns an error.

Add `TestUpdateClearsScalarFields`: update a context with all scalar fields, then call update with `ClearFields: []string{"summary", "goal", "current_state", "approach_summary", "working_directory"}`; assert those values are empty and workspace root is unchanged.

Add table tests for duplicate/unknown clear names and use with `resume`/`list`; assert each call errors and the saved revision/content is unchanged. Add a case that sets `summary` while listing `summary` in `ClearFields`; assert the clear wins and the revision increments once. Add a checkpoint case that clears `working_directory` and `approach_summary`; assert both the active handoff and new checkpoint contain the cleared state while workspace root is unchanged.

- [x] **Step 2: Run context tests and confirm the new contract is missing**

Run: `go test ./internal/contextstore -run 'TestResumeMigratesLegacyActiveReasoning|TestUpdateClearsScalarFields|TestUpdateRejectsInvalidClearFields|TestCheckpointClearsScalarFields' -count=1`

Expected: FAIL because `approach_summary` and `clear_fields` do not exist.

- [x] **Step 3: Add the new state/input fields and JSON migration**

Rename `State.ActiveReasoning` to `State.ApproachSummary`. Implement `UnmarshalJSON` for `State` that first decodes `approach_summary`, then uses legacy `active_reasoning` only when the new key is absent. Implement `UnmarshalJSON` for `Input` with the same key-presence precedence; its strict decoder accepts the known current fields plus the legacy alias and rejects every other unknown key. Keep only `ApproachSummary` as an `Input` struct/schema property. Because the Go MCP SDK validates against the schema before unmarshaling, generate the normal context schema with `jsonschema.For[contextstore.Input]`, set only its top-level `AdditionalProperties` to an empty permissive schema, and pass it in the context `mcp.Tool`. Promote the existing `github.com/google/jsonschema-go` module from indirect to direct in `go.mod`; do not add a new module version. `Input.UnmarshalJSON` then rejects all extra names except `active_reasoning`. In `tools/list`, assert `approach_summary` is a property and `active_reasoning` is not.

- [x] **Step 4: Validate and apply clears atomically**

Before mutating a context, validate `ClearFields` for allowed names, duplicates, and action. Apply ordinary supplied values, then clear every listed field so clear takes precedence over a value supplied in the same request. Treat `working_directory` as a context locator only. Pass both `ApproachSummary` and `ClearFields` through the checkpoint initialization path as well as update.

- [x] **Step 5: Update model-facing descriptions and run focused tests**

Update `context` tool descriptions in `internal/server/server.go` to name `approach_summary`, explain `clear_fields` precedence, and explicitly ask only for a concise approach summary. Add a client-level MCP test that calls context once with legacy `active_reasoning`, verifies the state stores its text as `approach_summary`, and verifies `tools/list` advertises `approach_summary` but not `active_reasoning`. Run:

```sh
go test ./internal/contextstore ./internal/server -run 'TestResumeMigratesLegacyActiveReasoning|TestInputUnmarshalMigratesLegacyActiveReasoning|TestInputUnmarshalRejectsUnknownFields|TestUpdateClearsScalarFields|TestUpdateRejectsInvalidClearFields|TestCheckpointClearsScalarFields|TestContextAcceptsLegacyActiveReasoningInput|TestUpdateResumeAndList|TestCheckpointSnapshotsActiveState|TestMCPNewTools' -count=1
```

Expected: PASS; new schemas advertise the new key, old stored/request keys remain readable, and invalid clears save no partial state. Commit with message `feat: add explicit context field clearing`.

### Task 3: Correct command and data-surface security wording

**Files:**
- Modify: `internal/server/server.go`
- Modify: `internal/config/config.go`
- Modify: `docs/SECURITY.md`
- Test: `internal/server/server_test.go`

**Interfaces:**
- Keep the workspace sandbox claim for file tools only.
- Keep commands on current-user permissions with `cwd` constrained through `Workspace.Resolve`.

- [x] **Step 1: Add exact description assertions**

Extend `TestMCPNewTools` to assert that the MCP implementation description, instructions, and `exec_command` description distinguish current-user command permissions from the workspace-contained `cwd`, do not claim an OS filesystem sandbox for commands, and still describe file-tool paths as workspace-restricted. Review the `config.Workspace.Root` comment and `docs/SECURITY.md` directly; there is no runtime assertion for Go comments.

- [x] **Step 2: Update descriptions and comments**

Change the MCP implementation description from “sandboxed file tools and commands” to wording that distinguishes workspace-sandboxed file tools from commands running as the current user. Change the `exec_command` description to state that the command runs with current-user permissions and has a workspace-contained working directory, but no OS filesystem sandbox. Change the config comment to “Root is the sandbox boundary for every file tool; command cwd is constrained here.” In `docs/SECURITY.md`, replace the false claim that `/api/edits` carries no secrets: say it can contain edited workspace text, is retained in memory only, and is protected by the configured local/origin, token, or explicitly unsafe access mode. Preserve the accurate no-secret statement for `/api/status` and `/widget`, clarify that MCP tool results may contain requested workspace content, and preserve that request inputs and outputs are omitted from logs. In the command section, replace “sandboxed subdirectory” with “workspace-contained subdirectory.” Keep the privilege-escalation guard documented as a guardrail, not an account sandbox.

- [x] **Step 3: Run server tests and commit**

Run: `go test ./internal/server -run 'TestMCPNewTools|TestHandlerLogsRequestsAndToolsWithoutPayloads|TestEditsEndpointFollowsAccessMode' -count=1`

Expected: PASS and no change to actual command permissions. Commit with message `docs: clarify command workspace boundary`.

### Task 4: Run the Phase A integration gate and synchronize verified records

**Files:**
- Verify: all Phase A files from the three implementation plans
- Create: `scripts/mcp-smoke/main.go`
- Update after evidence: Linear MIO-19/MIO-20, Notion `miodesk`, Slack `#miodesk`

**Interfaces:**
- Do not change MIO-13, MIO-14, or MIO-12 statuses in this task; they remain follow-on phases.
- Do not push, tag, publish a GitHub release, or overwrite an official release.

- [x] **Step 1: Run the complete local release gate**

Run: `scripts/release-check.sh`

Expected: all format, vet, module verification, race tests, clean build, and installer smoke checks pass.

Result on 2026-09-23: PASS after `scripts/mcp-smoke` was added. The smoke
client was subsequently tightened during review, rebuilt, and rerun against
the installed service; all checks passed. Production source files did not
change after the release gate.

- [x] **Step 2: Record build and source identity**

Run `git rev-parse HEAD`, `git status --short --branch`, `miodesk version`, and a read-only remote ref lookup for `origin/main`. If the remote lookup fails, record it as unverified and do not reuse the old `c6a6895` value as current.

Result: implementation source commit `06193366ae028f5f3500a6c68f8849c113bb92d6` on `mio/maintenance-phase-a`; read-only `origin/main` lookup returned `c6a6895dd79804702a7f480baa99c2bd59f2c6da`; nothing was pushed. Installed binary reports version `0.2.1-dev`, the implementation commit, and build date `2026-09-23T13:10:01Z`; SHA-256 `8bfb75d88724f4a9fe6beed730ace24701e97284c65a2afab4d5c21edf3f76b5`.

- [x] **Step 3: Verify the candidate MCP service and connector**

Add `scripts/mcp-smoke/main.go`, a small Go client using the existing official MCP SDK `StreamableClientTransport`. It accepts one `-endpoint` URL, connects, verifies exactly 10 tools and no retired command names, validates the `status` result, runs a short `exec_command`, starts `sleep 5; printf done` with a 1 ms yield and polls `write_stdin` until completion, then starts `cat`, writes a unique input line, verifies it in output, and cancels the pipe session with Ctrl-C and a nonzero exit code. It exits nonzero on any mismatch and prints only final check names, never command output beyond the fixed smoke marker.

Build and run the candidate before touching the installed service:

```sh
go build -buildvcs=false -o /tmp/miodesk-candidate ./cmd/miodesk
go build -buildvcs=false -o /tmp/miodesk-mcp-smoke ./scripts/mcp-smoke
/tmp/miodesk-candidate version
```

Start the candidate with isolated XDG state so it cannot overwrite the running service's state file. Choose an available loopback port with Python's standard-library `socket.bind(('127.0.0.1', 0))`, then run `/tmp/miodesk-candidate serve -host 127.0.0.1 -port "$port" -workspace "$PWD"` with `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_STATE_HOME`, and `XDG_CACHE_HOME` set beneath a fresh temporary directory. Measure time from process launch until `GET /healthz` returns 200; while the process is idle and before the smoke client connects, record RSS with `ps -o rss= -p "$pid"`; assert the candidate binary is below 20 MiB and idle RSS below 30 MiB. Run `/tmp/miodesk-mcp-smoke -endpoint "http://127.0.0.1:$port/mcp"`, then send SIGINT, wait for exit code 0, and remove only the temporary candidate directory.

Only after candidate checks pass, inspect `systemctl --user cat miodesk.service` and `systemctl --user status miodesk.service --no-pager`. Confirm the current unit still runs `/home/chillizu/.local/bin/miodesk`; if `ExecStart` differs, do not infer a new target. Save the current binary as `/home/chillizu/.local/bin/miodesk.pre-phase-a.<timestamp>` with its mode preserved, copy the candidate to a new file beside the target with mode 0755, and atomically rename it over the `ExecStart` path. Restart with `systemctl --user restart miodesk.service`. Read back the installed binary's `version`, user-service status, `miodesk status -json`, and live server `tools/list`; run the smoke client against the endpoint returned by the status command; exercise one short and one long command through the currently exposed connector. If any post-restart check fails, atomically restore the preserved binary to the `ExecStart` path and restart the user service before reporting. If ChatGPT still exposes the retired quartet, leave MIO-19 open and report the host schema separately.

The isolated candidate and installed user service passed all direct MCP checks, including exact 10-tool inventory, status, short exec, long exec polling, stdin echo/cancel, and graceful shutdown. The Miodesk ChatGPT project conversation confirmed that the host still exposes `miodesk_command`, `miodesk_command_start`, `miodesk_command_poll`, and `miodesk_command_cancel`; short and long command execution through those actions succeeded, but no stdin-writing action is available. This post-restart run did not reproduce the earlier `yield-time_ms` validation error; its cause remains unconfirmed. This connector readback is a historical pre-refresh snapshot. The user later confirmed that a new schema appeared after refreshing in a new conversation; exact action names and end-to-end behavior remain unverified in this task, so MIO-19 stays Todo. Do not repeat the refresh step.

- [x] **Step 4: Run the bounded host performance check**

Run the bounded benchmarks created in the other plans:

```sh
go test ./internal/server -run '^$' -bench '^(BenchmarkLegacyBodyLimit|BenchmarkRecordEditsBounded)$' -benchmem -benchtime=5x
go test ./internal/tools -run '^$' -bench '^BenchmarkDiffLinesBoundedDense$' -benchmem -benchtime=5x
```

Compile benchmark test binaries outside the measured process, then collect benchmark allocations/time and process peak RSS from Linux `/proc/<pid>/status` (`VmHWM`; `/usr/bin/time` is not installed on this host):

```sh
go test -c -o /tmp/miodesk-server-bench.test ./internal/server
go test -c -o /tmp/miodesk-tools-bench.test ./internal/tools
measure_peak_rss() {
  label=$1; shift
  "$@" &
  pid=$!
  peak_kb=0
  while kill -0 "$pid" 2>/dev/null; do
    state=$(awk '/^State:/ {print $2}' "/proc/$pid/status")
    current_kb=$(awk '/^VmHWM:/ {print $2}' "/proc/$pid/status")
    if [ -n "$current_kb" ] && [ "$current_kb" -gt "$peak_kb" ]; then peak_kb=$current_kb; fi
    [ "$state" = Z ] && break
    sleep 0.01
  done
  wait "$pid"
  printf '%s peak RSS %s kB\n' "$label" "$peak_kb"
}
measure_peak_rss request-cap /tmp/miodesk-server-bench.test -test.run '^$' -test.bench '^BenchmarkLegacyBodyLimit$' -test.benchmem -test.benchtime=5x
measure_peak_rss dense-diff /tmp/miodesk-tools-bench.test -test.run '^$' -test.bench '^BenchmarkDiffLinesBoundedDense$' -test.benchmem -test.benchtime=5x
```

Record elapsed time, allocations, and peak RSS. This is host-side resource evidence, not board-level validation; no serial device is attached.

Result (`-benchtime=5x`): `BenchmarkLegacyBodyLimit` 9.60 ms/op, 36,236,118 B/op, 61 allocs/op, 119,092 KiB peak RSS; `BenchmarkRecordEditsBounded` 36.8 µs/op, 164,910 B/op, 32 allocs/op, 15,436 KiB peak RSS; `BenchmarkDiffLinesBoundedDense` 4.25 µs/op, 0 B/op, 0 allocs/op, 9,008 KiB peak RSS. The request-cap allocation/RSS is recorded as a follow-up concurrency-budget signal, not hardware validation.

- [x] **Step 5: Update external records from verified results**

At the time of this implementation record, Linear MIO-19 documented the old host schema, successful short/long legacy-action calls, non-reproduction of the earlier parameter error, and a pending Refresh step; it remained Todo. MIO-20 remains Done. The Notion `miodesk` page and the Slack `#miodesk` status thread reflected that same pre-refresh snapshot. No private local endpoint or credentials were included.

Follow-up · 2026-09-23: The user later manually confirmed that a new schema appeared after reinstalling/refreshing in a new Miodesk conversation. This supersedes the old-schema and pending-refresh status above. MIO-19 remains Todo only for exact action-list and refreshed `exec_command` / `write_stdin` short/long-session verification. The repository spec, Linear project and MIO-19, Notion project page, and Slack `#miodesk` were updated and read back; no further refresh is requested.

- [x] **Step 6: Verify final repository state**

Run `git status --short --branch`, `git diff --check`, and `git log -1 --oneline`. Expected: no untracked build outputs, only intentional Phase A commits, and no whitespace errors. Do not push or publish.

Result after the final local audit commit: branch `mio/maintenance-phase-a`,
no untracked build outputs, `git diff --check` clean, and the final repository
state was read back. No push or publication occurred.
