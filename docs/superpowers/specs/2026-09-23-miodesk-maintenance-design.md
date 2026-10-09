# miodesk Reliability Maintenance Design

Date: 2026-09-23

Status: Phase A local implementation, release gate, candidate MCP smoke,
service deployment, and host-side performance checks completed on 2026-09-23.
The earlier connector readback below is historical: the user later manually
confirmed that a new schema appeared after refreshing in a new conversation.
This task has not independently called the refreshed actions, so MIO-19 stays
open until the exact action inventory and `exec_command` / `write_stdin`
end-to-end behavior are verified. Do not repeat the refresh step.

## 1. Intent and success criteria

The goal is to make miodesk predictable under stale clients, unusually large
requests, dense edits, and incomplete embedded UI assets, while keeping its
workspace boundary and Native-first tool behavior clear. The maintenance must
also reconcile source, deployed binary, and the ChatGPT connector's visible
tool schema using evidence from each layer.

Success means:

- HTTP request size and read time, edit work/results, edit history, and stdin
  writes have explicit limits.
- A partial builtin search is reported as partial; it never looks complete
  after silently stopping early.
- The legacy command shim takes tool identity from the MCP request body,
  handles stale extra fields, and never advertises retired tools.
- Status remains callable without UI resources or metadata, and actual tool
  counts match `tools/list`.
- Model-facing descriptions match the real security boundary. Commands run as
  the current OS user with a workspace-contained working directory; they do
  not run in an OS filesystem sandbox.
- Context state has an explicit way to clear scalar fields and uses a compact
  approach summary rather than a field name that suggests preserving full
  reasoning traces.
- A release candidate passes the repository release gate and its build
  identity, running service, MCP `tools/list`, and connector schema are
  checked separately.

The design was drafted from source baseline `44ec2b87`. Phase A code now lives
on branch `mio/maintenance-phase-a` at implementation commit
`06193366ae028f5f3500a6c68f8849c113bb92d6`; the smoke client and final audit
documents are maintained as separate local commits. On 2026-09-23, a read-only
`git ls-remote` confirmed `origin/main` at `c6a6895dd79804702a7f480baa99c2bd59f2c6da`.
Nothing was pushed.

The final `scripts/release-check.sh` passed after adding `scripts/mcp-smoke`.
The installed user service is active on the Phase A binary, which reports
`0.2.1-dev`, the implementation commit above, and build date
`2026-09-23T13:10:01Z`. Its SHA-256 is
`8bfb75d88724f4a9fe6beed730ace24701e97284c65a2afab4d5c21edf3f76b5`.
The current server returned exactly 10 tools with no retired command quartet;
status, a short command, a polled long command, stdin echo/cancel, and graceful
shutdown all passed over Streamable HTTP MCP. The service is active and the
OpenAI tunnel reports running.

The current Codex task did not expose a miodesk connector tool, and binding the
supplied ChatGPT project browser tab timed out. A separate verification
conversation in the `Miodesk` ChatGPT project read the actual host tool list
after service restart: it still exposes `miodesk_command`,
`miodesk_command_start`, `miodesk_command_poll`, and
`miodesk_command_cancel`, not unified exec names. Short `printf` and long
`sleep 2; printf` calls both completed through those legacy actions; the long
result uses `session_id`, but no stdin-writing action is available. No
additionalProperties error occurred in this check. The earlier
`yield-time_ms` validation error is a historical observation and its root cause
remains unconfirmed. At the time of this recorded readback, official ChatGPT
guidance showed that a workspace owner/admin could use Workspace settings →
Apps → Miodesk → … → Action control → Refresh, review the action diff, enable
the current unified actions, and save; that control was not available from the
connector conversation. This pre-refresh snapshot is superseded by the user's
later report that a new schema appeared in a refreshed conversation. Treat
that report as the current schema-refresh evidence; exact tool names and
command/session behavior still need an action-level smoke. MIO-19 remains open
for that validation without repeating the refresh step. There are no serial
devices attached, so board-level testing is not part of this phase.

## 2. Chosen approach and decomposition

Three scopes were considered:

1. Fix only connector migration. This is the smallest change, but leaves
   independently confirmed resource and correctness defects in place.
2. Recommended: complete the connector and reliability hardening as Phase A,
   then handle the existing permission/update work and release automation in
   separate reviewable phases. This addresses current runtime risk first and
   avoids combining unrelated release policy with tool behavior changes.
3. Combine all hardening, OAuth, Windows service/ConPTY, and CatDesk
   consolidation into one release. This would mix independent architecture
   decisions and enlarge the verification surface unnecessarily.

This document specifies Phase A. The approved program order is:

- **Phase A — reliability and interface hardening:** MIO-20 and the local
  implementation/server checks for MIO-19, resource-bound and search issues
  found in the audit, security wording, edit-history handling, file creation
  mode, and context field semantics. The connector migration acceptance for
  MIO-19 remains open pending action-level verification of the user-confirmed
  refreshed schema; no further host refresh is needed.
- **Phase B — permissions and update behavior:** MIO-13 permission profiles
  and read-only mode, followed by MIO-14's default official update source.
  Each receives its own design and implementation plan after Phase A.
- **Phase C — release automation:** MIO-12 tag-triggered release and
  cross-platform CI, with its own design and plan after Phase B.

OAuth 2.1 for custom connectors, Windows ConPTY/service/path support, MIO-10
production-auth confirmation, and MIO-11 CatDesk directory consolidation
remain follow-up work. They are not prerequisites for this maintenance
program.

## 3. Phase A architecture

### 3.1 HTTP request and legacy migration path

The request path remains:

```text
HTTP request
  -> request logging and existing Origin/auth checks
  -> bounded POST body read on /mcp
  -> legacy request normalizer
  -> official MCP Go SDK handler
  -> typed tool handler
```

The compatibility layer remains a temporary adapter for the four previously
advertised command calls. It does not add old tools back to `tools/list`.

The MCP body is the source of truth for `params.name`; `Mcp-Name` is only a
mirrored transport header. A missing or mismatched header must not change the
legacy command's timing defaults. Observed unified-only leaked fields
(`yield-time_ms`, `yield-time-ms`, and `tty`) are removed from legacy
arguments before typed validation, while fields defined by the retired schema
are translated only according to the existing compatibility contract.

Proposed HTTP limits are 16 MiB per POST body and 30 seconds to read a complete
request. Oversize input returns HTTP 413; malformed JSON returns HTTP 400. The
read timeout bounds request acquisition and must not cap tool execution after
the request body has been consumed. Responses must not echo request contents.

### 3.2 Tool work and result budgets

Apply limits before allocating line tables, diff operations, or cached
snapshots:

| Surface | Proposed bound | Behavior at the limit |
| --- | ---: | --- |
| MCP POST body | 16 MiB; 30-second read deadline | Reject with 413 or 400 |
| `write_stdin.chars` | 64 KiB per call | Reject clearly before writing |
| Edit operations | Existing 100-operation cap | Reject larger batch |
| Existing edit file | Existing 2 MiB cap | Reject before reading |
| New content per edited file | 2 MiB | Reject before commit |
| Aggregate old/new edit content per batch | 16 MiB | Reject before commit |
| Generated diff | 20,000 lines and 1 MiB serialized per batch | Return a compact summary with `diff_truncated: true` |
| In-memory edit history | 10 records and 2 MiB total diff data | Evict oldest records to stay within both bounds |

Edit validation, new-content validation, and diff budgeting happen before the
atomic commit. Exceeding a diff-output budget does not leave a partially
written file: the edit is either validated and committed atomically with an
explicitly truncated diff summary, or rejected before commit when content
limits are exceeded. The MCP edit result and `/api/edits` use the same
truncation indication.

`write_stdin` has a 5-second maximum write interval and must honor request
cancellation/session termination. A blocked child process must not hold the
session write lock indefinitely or leave unbounded writer goroutines. The
existing yield-time behavior continues after the input write completes.

The builtin search fallback may retain its existing per-file scan cap, but it
must report `Truncated=true` when the cap is reached or scanning stops on an
error such as an overlong token. This applies to both a selected file and a
directory walk, including when fewer than the match-count limit were found.

### 3.3 Server status, security, and file behavior

- Register `status` as a read-only structured tool without MCP Apps UI
  metadata or resources. Keep its schema and invocation labels; all ten tools
  remain in `tools/list`, while `resources/list` is empty. Retain the JSON
  diagnostics APIs and return `"auto"` in `/api/status.theme` for compatibility.
- Describe commands as running with the current user's permissions and a
  workspace-contained `cwd`. Keep the stronger canonical-path workspace
  boundary statement for file tools.
- `/api/edits` remains behind the existing access middleware. Its response may
  contain edited workspace text and is therefore sensitive. Update
  `docs/SECURITY.md` to say this directly, preserve the in-memory-only
  retention model, enforce the history byte budget above, and add token-mode
  authorization tests. Do not add edit contents to logs or status tool output.
- New files use standard creation permissions filtered by the process umask;
  replacing an existing file preserves its permission bits. Atomic temp-file
  replacement remains intact.

### 3.4 Context field contract

Replace model-facing `active_reasoning` with `approach_summary`, described as
a concise summary of the current approach and rationale. Existing persisted
contexts using `active_reasoning` remain readable and are rewritten using the
new field on their next update. Incoming legacy JSON with `active_reasoning`
is accepted as an alias during the compatibility period, but the generated
schema advertises only `approach_summary`.

Add `clear_fields` to context `update` and `checkpoint`. It accepts only
`working_directory`, `summary`, `goal`, `current_state`, and
`approach_summary`. Listing a field clears it; omitting it leaves the current
value unchanged. Clearing `working_directory` removes only the context
locator and never changes the configured workspace root. Unknown or duplicate
field names return a validation error without saving a partial update.

List-valued fields retain the current contract: omitted (`nil`) means no
change; an explicit empty list clears the list.

## 4. Error handling and compatibility

- Every limit error identifies the affected tool or HTTP surface and the
  applicable maximum, without returning file contents or credentials.
- Legacy middleware accepts only the old command quartet and does not invent
  new options. Current unified calls pass through unchanged.
- Context migration is read-compatible for old persisted state and old
  requests. New state and schemas use `approach_summary`.
- The `/api/edits` byte cap affects only its recent-history response. It does
  not change the atomic edit result returned directly by the tool, beyond the
  explicit diff truncation contract.
- No code path logs request bodies, command stdin, file contents, or auth
  headers.

## 5. Verification and completion gates

Phase A is complete only when all of these pass:

1. Unit and integration tests cover oversized/slow MCP bodies, body-name
   authority with missing/mismatched `Mcp-Name`, and stripping stale fields.
2. Dense-newline and multi-file edit cases remain within the documented input,
   allocation, output, and cache budgets; atomicity and `diff_truncated` are
   asserted.
3. `write_stdin` rejects oversized input and returns from blocked writes on
   timeout/cancellation without wedging later session operations.
4. Builtin search tests prove that reaching the fallback's byte limit or
   hitting a scanner error sets `Truncated=true`, including when the number of
   returned matches is below the match-count limit.
5. Tests prove status remains callable with no UI resources, actual
   registration count matches `tools/list`, command descriptions avoid the
   sandbox claim, and new/overwritten file permissions follow the contract.
6. Context tests cover clear and no-change semantics, unknown clear fields,
   legacy persisted JSON, and legacy request aliasing.
7. `/api/edits` tests cover local/token/unsafe access modes and bounded
   retention; security documentation accurately describes its contents.
8. `scripts/release-check.sh` passes on the reviewed candidate.
9. Record current upstream refs if reachable, binary version/commit/build,
   service status, server `tools/list`, and ChatGPT connector tool schema.
   Exercise a short command and a long-command start/poll/input/cancel flow
   through the current connector. If the connector still serves cached tools,
   report that as a separate host/schema blocker rather than claiming MIO-19
   complete.

The performance check is a bounded host-side stress run measuring peak
allocations/RSS and response time for request, dense-diff, and edit-history
limits. It is not hardware validation. Board-level testing can be added only
when serial hardware is available.

This design did not authorize pushing commits, creating a tag, publishing a
GitHub release, or overwriting an official release. The implementation plan
later completed local build/test and reversible service verification; any
publication remains a separate approval gate.

## 6. Review notes

- MIO-19's earlier claim that the `yield-time_ms` failure is currently
  reproduced is stale: the post-restart short and long calls succeeded through
  the legacy connector surface. The early error remains historical and its
  cause is unconfirmed. The old-schema observation in this plan is also a
  pre-refresh snapshot; the user later confirmed that a refreshed schema
  appeared. Exact action inventory and new-surface command/session behavior
  remain open for MIO-19.
- MIO-20's header/body mismatch is included: the parsed body tool name is the
  semantic source of truth.
- The current GitHub `main` ref was checked read-only on 2026-09-23 and
  returned `c6a6895dd79804702a7f480baa99c2bd59f2c6da`; later reports should
  perform another read before treating it as current.
- ChatGPT connector action refresh guidance was checked against the [official
  MCP apps documentation](https://help.openai.com/en/articles/12584461-developer-mode-and-mcp-apps-in-chatgpt)
  on 2026-09-23; workspace UI labels can change.
- Linear, Notion, and Slack synchronization is tracked by the final integration
  step and must use only the verified status above.

## 7. Implementation and performance record · 2026-09-23

The candidate binary was 15,979,763 bytes. In an isolated local service run,
health became available in 6 ms and pre-request idle RSS was 16,124 KiB. After
installation, `/proc` reported 23,344 KiB RSS for the running service. The MCP
smoke verified all 10 current tools, status, short exec, long exec polling,
stdin echo/cancel, and clean shutdown. The service binary hash and source
identity are recorded above.

Five-iteration host benchmarks (`-benchtime=5x`) measured:

| Benchmark | Time/op | Allocations/op | Peak RSS |
| --- | ---: | ---: | ---: |
| `BenchmarkLegacyBodyLimit` | 9.60 ms | 36,236,118 B / 61 allocs | 119,092 KiB |
| `BenchmarkRecordEditsBounded` | 36.8 µs | 164,910 B / 32 allocs | 15,436 KiB |
| `BenchmarkDiffLinesBoundedDense` | 4.25 µs | 0 B / 0 allocs | 9,008 KiB |

The request-limit benchmark rejects a body one byte over 16 MiB after reading
up to the configured cap. Its aggregate heap/RSS cost is a capacity signal for
a later concurrency-budget review; this phase establishes a per-request cap
but does not add global request admission control. These are host-side results,
not hardware validation.
