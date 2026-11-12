# RFC: miodesk DSH Native Agent Adapter

Status: Initial external plugin source and Core stdio plugin bridge implemented; isolated mock-host tests run. **Not deployed to the user service, and no real DSH prompt send has been accepted.**
Checked: 2026-11-12. Target: miodesk (Go) talking to the **existing** DSH Web Host.
Owner: miodesk project. DSH under test: `@deepseek-ai/dsh@0.2.1-rc.5`; miodesk: `0.2.3-panel-dev`.

## 1. Decision

Prefer the DSH Host's **authenticated, non-browser API Peer** and generated Remote RPC, rather than Chrome DevTools, Playwright, DOM automation, or a second SDK Agent runtime. The implementation is a separate Go repository (`Chillizu/miodesk-dsh`) exposed through Core's generic, opt-in external stdio MCP plugin bridge; it is not embedded in Core.

```text
MCP client
  -> existing miodesk MCP transport, auth, audit
    -> optional DSH Adapter (permission + session scope + submission ledger + cursor)
      -> loopback HTTP /api/<namespace>/<method> and WebSocket /api/remote.mux
        -> existing DSH Host controllers and durable Session log
```

MCP tool registration is opt-in. The Core's existing ten stable tools stay unchanged by default. No WebUI login/profile copy and no Chrome debugging port.

## 2. Empirical read-only findings (no production mutation)

On the connected Linux host: DSH systemd service running at `127.0.0.1:3080`, miodesk at `127.0.0.1:8787/mcp`. Unauthenticated `GET /` and `GET /api` returned HTTP 401.

DSH 0.2.1-rc.5 supports `GET /connect?token=...&interactions=none`, which returns 302 with an API Peer cookie and the WebSocket mux location. Authenticated WebSocket to `/api/remote.mux` succeeded. HTTP POST `/api/session/list` succeeded with generated argument key `_request`; the first probe returned 82 Sessions. A later read returned 83 (the live catalog changes over time). The same connection read `session/projections` and `session/page` and received a `session/follow` snapshot with a cursor and projection baseline. `session/modelCatalog` returned four provider groups and 35 models; `workspace/follow` returned a real baseline including workspace and archived/pinned Session sets. **No message was sent, no service was restarted.** Auth secrets were held only inside the local probe process; only aggregate checks were printed.

Current sources:
- https://github.com/deepseek-ai/deepseek-harness/blob/master/packages/client/connection/README.md
- https://github.com/deepseek-ai/deepseek-harness/blob/master/packages/api/session-controller/README.md
- https://github.com/deepseek-ai/deepseek-harness/blob/master/packages/api/gateway/README.md
- https://github.com/deepseek-ai/deepseek-harness/blob/master/docs/subsystems/web-client.md
- Locally installed npm package typings under `@deepseek-ai/dsh-api-session-controller/lib/types`, generated `typert.remote-client.d.ts`, and gateway stream protocol.

## 3. API contract actually confirmed in the installed build

Authentication:
- `GET /connect?token=<process-launch-token>&interactions=none` creates an API Peer, returns 302 and an **API** cookie. Attach the same cookie to a WebSocket `/api/remote.mux`; keep this socket alive for HTTP RPC. Peer lifetime is tied to this socket.
- The API Peer is distinct from the browser session; never scrape browser cookies or reuse Chrome profiles.
- Loopback is not an authentication bypass. API token and cookie are secrets.

Unary RPC envelope:
```json
{"type":"client-request","rpcId":"<uuid>","method":"session/list","payload":{"args":{"_request":{}}}}
```
`POST /api/session/list` with `Content-Type: application/json`, authenticated cookie.
Response: `{"type":"server-response","rpcId":"same","result":{"ok":true,"value":{"items":[...]}}}`, or typed `result.error`. Validate `rpcId`, response type, bounded size, and exact endpoint. Do not log the full error/payload because tool output can contain secrets.

Known native methods (the field name is generated from the actual TypeScript argument):
- `session/list`: `{_request:{}}`; summaries include `sessionId,cwd,updatedAt,running,blank,agentAvailable` and optional cached projections.
- `session/projections`: `{request:{sessionId}}`; return `kind:sequenced,asOfSeq,values`, `migration-required`, or null.
- `session/page`: `{request:{address:{kind:"session",sessionId},throughSeq,maxMessages}}`; requires a meaningful inclusive cut, ideally the `follow` cursor.
- `session/modelCatalog`: `{}`; available provider/model/effort routes.
- `session/selectModel`: `{request:{sessionId,provider,model,reasoningEffort?}}`; mutating, opt-in.
- `session/prompt`: `{request:{sessionId,requestId,mode:"queue"|"steer",content:[{type:"text",text}]}}`; returns only `{accepted:true}`, **NOT a turn ID**.
- `session/cancel`: `{request:{sessionId}}`; cancels the live ordinary Agent turn, requires elevated explicit permission.
- `session/create`, `session/search`, `workspace/follow`, `job/list` can support later work.
- WebSocket mux message `{type:"open",streamId,endpoint:"session/follow",payload:{args:{request:{address:{kind:"session",sessionId}}}}}`; receive `{type:"item",streamId,value:{type:"snapshot",cursor,records,projections,...}}`, then events. Use `{type:"cancel",streamId}` to stop following.

Important: SDK JSON-RPC server exists but opens SDK-owned Sessions; do not assume it attaches to already running WebUI Sessions. Native Gateway directly targets the existing Host, so it is the first choice.

## 4. External MCP tools (minimal core first)

Tool | Permission | Behavior
--- | --- | ---
`dsh_status` | read | Connection generation, Host reachability, DSH version if independently available; never credentials.
`dsh_sessions` | read | Workspace-scoped catalog with stable native session IDs and explicit cursor/limits.
`dsh_read` | read | Cursor-based event history. Include durable seq, event type, bounded content and tool result status; never silently truncate.
`dsh_send` | send | Single session-targeted non-steering prompt; require explicit workspaceId+sessionId and caller-supplied idempotency key; reject obvious busy state, then use DSH mode `queue`.
`dsh_turn_status` | read | Resolve submission to native log events; return observed state/terminal outcome; never fake generation vs tool phase.
`dsh_events` | read | Bounded incremental cursor polling; streaming subscription optional later.
`dsh_cancel` | cancel | Explicitly cancel a known **active** turn after identity and permission checks; never kill a process or service.
Service restart | separate privileged action | Not part of normal session adapter; guard service identity, task state and user authorization.

Recommended MVP register only `dsh_sessions`, `dsh_read`, `dsh_send`, and `dsh_turn_status`, plus optional `dsh_status`. Add others after successful flow.

## 5. Identity and idempotency

- Native `sessionId` remains opaque and stable. `session/follow` / `session/page` event `seq` is the **durable per-session cursor**; WebSocket `streamId` is only ephemeral.
- Mint one `requestId` per requested submission and never replace it during retry. DSH's Session Controller deduplicates a `requestId` already queued or recorded, returning the previous acceptance.
- `session/prompt` only returns acceptance. A user-facing `turn_handle` can be adapter-owned (= submission/request identity) **and MUST be explicitly labeled as such**. Do not claim the native `turn/start.data.turn` is known until a later event correlates it to `user/message.data.source.rpcId`.
- Durable adapter ledger stored outside repos, e.g. XDG state under `miodesk/dsh/`; store `caller_key,host_identity,workspace_id,session_id,payload_hash,request_id,status` with atomic 0600 write. Never store bearer cookies, prompt text, or full event content there.
- Reused caller key with different payload, session or model => conflict, not a second submission.
- On HTTP timeout or lost WS, report `acceptance_unknown`, re-establish API Peer and retry **the same native requestId**. Never invent a new UUID on retry.
- Multi-client contention: use a per-session adapter mutex to avoid two **adapter** callers racing, but recognize the WebUI remains an external writer. The separate list/check and prompt calls are not an atomic compare-and-submit transaction; `mode:"queue"` avoids interrupting a running turn but may enqueue behind a newly busy turn. For strict reject-if-busy semantics, DSH must expose an atomic conditional-admission operation; do not claim race-free idle enforcement until then.

## 6. State semantics

Native ground truth:
- `session.list.running` signals a live Agent is active, not its exact work phase.
- `turn/start` and `turn/end` are durable; `turn/end.data.reason.kind` is one of `completed,aborted,blocked,error,max-tokens,interrupted,forked` (plugins may extend).
- `tool/call`, `tool/result`, `step/start`, `step/end` and `job/registered|settled` show durable tool and execution boundaries.
- `session/projections.values.userQuestions` contains `active,settled`, but a timed question can remain answerable **without blocking** an Agent. Do not classify every `active` question as `waiting_user`.
- Transient `assistantStream` in `session/follow` and optional `session/control` provide narrower live hints; a pure event-log snapshot cannot authoritatively distinguish every model/token/tool/approval phase.

Expose:
```json
{
  "session_id":"...",
  "turn_handle":"adapter-generated-id",
  "native_turn":null,
  "state":"pending|running|waiting_user|completed|failed|cancelled|unknown",
  "phase":"model|tool|awaiting_user|idle|unknown",
  "source":"durable_event|host_projection|live_stream|inference",
  "cursor":123,
  "terminal_reason":null
}
```
Use `unknown` when a state is not proven. A final `turn/end` with `completed` maps to completed, `aborted` to cancelled, `error`/ `blocked`/ `interrupted` to failed or interrupted with raw structured reason preserved. `max-tokens` must be distinct from success. Tool result errors may still be followed by a successfully completed turn; do not infer failed from one tool error.

Exact `model vs tool vs approval` live phase is a **follow-up capability** if not established via native streams; consider a tiny DSH Host lifecycle plugin exporting definitive phase events rather than guessing.

## 7. Security and deployment

- Optional adapter OFF by default and READ-ONLY by default. Explicit, separately gated privileges: `read`, `send`, `cancel`; service management is a different authorization domain.
- Scope access by exact registered workspace identity and session ID; do not trust only a path prefix, browser tab title, or a caller-supplied session name. Re-check ownership before every mutation and on reconnect.
- DSH base URL fixed to an approved loopback authority; refuse arbitrary public endpoints, redirects to other hosts, cross-host cookies, credential echo, and mixed profiles.
- Never serialize cookies, launch tokens, complete authenticated URLs, raw auth headers, or unfiltered Agent tool outputs to MCP logs, trace reports, or error strings. Bound read sizes, event counts, timeouts, and concurrency.
- Never request Chrome DevTools access or copy a real Chrome user profile.
- The `/connect` launch token is process-scoped and rotates on restart. **Journal scraping was used only for this private read-only protocol PoC, not selected as an operational contract.** For production, use a privileged local pairing/credential broker managed by the DSH service owner and accessible to miodesk through a private OS path (0600 file or owner-only Unix socket), with revocation and rotation after DSH restart. Treat an unavailable broker as `disconnected`, never relax auth.
- The API Peer cookie is in memory only and tied to its WebSocket. On reconnect: renew Peer, reacquire scoped baselines and resume from durable cursor. An unmatched profile/generation must not replay a submission against a different Host.
- Avoid passing secrets as command-line arguments where `ps` may reveal them.

## 8. MVP acceptance procedure

1. In an isolated test workspace, pair to an existing DSH Host through an approved local credential source; open non-browser API Peer.
2. `dsh_sessions` resolves exact workspace and target session; `dsh_read` obtains a durable follow cursor.
3. Confirm idle from fresh Host state; acquire per-session adapter lock. If busy, return a structured conflict rather than steering.
4. Persist idempotency `requestId` before sending; optionally `selectModel` once on the same validated Session; call `session/prompt` with `mode:"queue"`.
5. Return `accepted` and adapter `turn_handle`, with native turn marked pending. Correlate `user/message.source.rpcId` and later `turn/start` to resolve the native turn.
6. Consume `follow` event stream, persist latest contiguous seq, recover gaps via native snapshot/page, resolve `turn/end`, and return full bounded transcript/result with explicit truncation/pagination flags.
7. Induce a response-loss test: retry using the same `requestId`, verify exactly one message was admitted. Also test busy session, wrong workspace, failed/aborted turn, WS drop, Host restart, credential expiry, permission denial.
8. Compare final Git/testing/service facts **independently** via miodesk's existing tools; DSH's self-report is not external verification.

Completion requires a real isolated no-browser send and durable terminal read, plus automated protocol/permission/reconnect tests. **The current investigation does not yet meet the send/retry acceptance criterion.**

## 9. Implementation slices and remaining blockers

1. **Complete:** verify current DSH native API authentication, HTTP list/projections/page, WebSocket follow and persistence boundaries.
2. **Partial:** opt-in Go stdio adapter and owner-only token-file pairing implemented in separate `miodesk-dsh` repo. A rotation-aware broker remains future work; do not scrape the journal in the adapter.
3. **Implemented:** initial MCP read-only tools, workspace membership checks, query limits and mock DSH fixtures; live production re-validation remains.
4. **Mock-tested only:** `dsh_send` with durable ledger and `dsh_turn_status`; still requires isolated real-Host send/turn completion test (never use a long-running user session).
5. Later: model/preset selection UX, user approvals, exact phase Host instrumentation, events, cancellation, attachments and optional packaging.

Compatibility caution: DSH is a developer preview with breaking changes. Pin the tested DSH minor/RC, perform startup capability/protocol checks, and **fail closed** on unexpected response/schema changes.

## Public-tool consolidation (2026-11-12)

The independent connector exposes four cohesive workflow tools:

| Public stdio MCP name | Consolidates |
| --- | --- |
| `list_sessions` | old status/sessions discovery; workspace membership and aggregate status |
| `get_session` | read/history and Session inspection; recent message text, event cursor, optional event metadata |
| `send_message` | formerly `dsh_send`, unchanged explicit write gating, idle check and native `requestId` idempotency |
| `get_execution_status` | formerly `dsh_turn_status`, plus session-level running/last-terminal state without a submission ID |

The miodesk Core namespace is `dsh__<tool>`. `cancel_execution` remains separately privileged and opt-in. The public tool count stays small; internal RPC helpers and history/event transport are not separate daily-use tools. Native real-Host **send** acceptance and terminal tracking remain outstanding.
