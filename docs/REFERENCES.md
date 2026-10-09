# REFERENCES

Official documentation verified for this project. One entry per topic: what it
is, where it lives, and the decision it drives. Do not copy documentation text
here — link it.

Format: topic → URL → version → used by → decision → checked.

---

## MCP Go SDK

- URL: https://github.com/modelcontextprotocol/go-sdk (v1.7.0)
- Docs: https://github.com/modelcontextprotocol/go-sdk/blob/main/docs/quick_start.md
- Used by: `internal/server`
- Decision: Use the official SDK for the entire MCP protocol stack —
  `mcp.NewServer`, `mcp.AddTool` (typed handlers), `mcp.StdioTransport`,
  `mcp.NewStreamableHTTPHandler`. miodesk must not re-implement JSON-RPC/MCP.
- Verified API (from module cache source, v1.7.0):
  - `mcp.NewServer(&mcp.Implementation{Name, Version}, nil) *Server`
  - `mcp.AddTool(s, &mcp.Tool{Name, Description}, func(ctx, *mcp.CallToolRequest, I) (*mcp.CallToolResult, O, error))`
  - `server.Run(ctx, &mcp.StdioTransport{})` — blocking, stdio transport
  - `mcp.NewStreamableHTTPHandler(func(*http.Request) *Server, *StreamableHTTPOptions)` — http.Handler; session handling per MCP spec
  - Client (tests): `mcp.NewClient`, `client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint, …}, nil)`, `&mcp.CommandTransport{Command: exec.Command(…)}`
- Checked: 2026-09-07

## MCP Specification

- URL: https://modelcontextprotocol.io/specification/2026-07-28/index.md
- Transports: https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/streamable-http.md
- Index: https://modelcontextprotocol.io/llms.txt
- Used by: `internal/server`, `internal/tools`
- Decision: SDK v1.7.0 implements the transport/session behavior (including the
  stateless direction, SEP-2567); miodesk does not hand-roll MCP transport
  sessions or SSE. The 2026-07-28 spec uses full JSON Schema 2020-12 for tools
  and recommends explicit application handles when state must survive between
  otherwise stateless requests. Miodesk therefore threads command state through
  a numeric `session_id` returned by `exec_command` and passed to `write_stdin`.
  Typed `mcp.AddTool` handlers derive input and output schemas from Go types;
  current Streamable HTTP protocol headers are left to the SDK.
- Checked: 2026-09-21 (SDK source and current spec)

## OpenAI / ChatGPT MCP

- URL: https://developers.openai.com/llms.txt
- ChatGPT UI: https://developers.openai.com/plugins/build/chatgpt-ui
- Used by: `internal/adapter`, `internal/cli` deployment guidance
- Decision: Invocation labels and structured `status` registration stay in the
  adapter layer; no MCP Apps UI resources are published. Core tools and
  transports stay host-agnostic. ChatGPT connectors
  document `noauth` and OAuth 2.1-style flows; a static bearer token is not a
  documented connector authentication choice.
- Checked: 2026-09-07

## OpenAI Plugin Creator packaging

- URLs:
  - https://developers.openai.com/plugins/build/plugins
  - https://developers.openai.com/plugins/concepts/plugins
  - https://developers.openai.com/plugins/build/chatgpt-ui
  - https://developers.openai.com/plugins/deploy/connect-chatgpt
  - https://developers.openai.com/plugins/deploy/submission
- Used by: `plugins/miodesk`
- Decision: use the portable Agent Plugins 1.0 root `plugin.json`, with
  `extensions.com.openai.apps` mapping `.app.json` to the registered Developer
  Mode MCP app. Keep `.codex-plugin/plugin.json` as a compatibility overlay.
  Skills and visual assets package the lightweight workflow and identity; they
  do not replace the Go server, MCP connection, or Secure MCP Tunnel. MCP Apps
  UI is optional and should be added only for workflows where inline interaction
  helps; existing Native-first tools remain useful without widgets. Refresh MCP
  metadata after server schema changes, then test in a new conversation.
  Public-directory submission is separate and requires a stable public HTTPS
  endpoint; Secure MCP Tunnel alone does not satisfy it.
- Checked: 2026-09-23

Public onboarding intentionally exposes OpenAI Secure MCP Tunnel as the
default connection. The legacy provider implementations listed below remain
for compatibility and tests; they are not enumerated by the primary setup
flow.

## OpenAI Codex unified exec

- URLs:
  - https://github.com/openai/codex/blob/main/codex-rs/core/src/tools/handlers/unified_exec.rs
  - https://github.com/openai/codex/blob/main/codex-rs/core/src/tools/handlers/unified_exec/exec_command.rs
  - https://github.com/openai/codex/blob/main/codex-rs/core/src/tools/handlers/unified_exec/write_stdin.rs
  - https://github.com/openai/codex/blob/main/codex-rs/features/src/lib.rs
- Used by: `internal/tools`, `internal/server`
- Decision: keep the model-facing terminal vocabulary close to Codex's unified
  execution model: `exec_command`, numeric `session_id`, `write_stdin`, and an
  opt-in `tty` flag. Pipe mode remains the default so ordinary commands keep
  deterministic output; `tty=true` allocates an interactive Unix PTY. tmux
  remains something a command may invoke when installed, not a required
  dependency or a first-class MCP tool. A temporary server-side migration shim
  may translate the four formerly advertised command lifecycle calls from stale
  client caches, but retired names are never returned by `tools/list` and the
  shim is not part of the long-term model-facing API.
- Checked: 2026-09-23

## creack/pty

- URL: https://github.com/creack/pty (v1.1.24)
- Used by: `internal/tools/pty_unix.go`
- Decision: use the small MIT-licensed PTY package only behind `tty=true` on
  Unix. Ordinary commands remain pipe-backed; Windows keeps compiling with an
  explicit unsupported-TTY backend until a native ConPTY implementation is
  justified. PTY starts at a conservative 120x40 terminal size and remains an
  internal execution detail rather than a separate model-facing tool.
- Checked: 2026-09-22

## XDG Base Directory Specification

- URL: https://specifications.freedesktop.org/basedir-spec/latest/
- Used by: `internal/xdg`, `internal/config`
- Decision: `$XDG_{CONFIG,DATA,STATE,CACHE}_HOME/miodesk` with `~/.config`,
  `~/.local/share`, `~/.local/state`, `~/.cache` fallbacks; relative
  `$XDG_*` values are rejected. Runtime state (server.json) goes to the state
  dir, never into config.toml.
- Checked: 2026-09-06

## ripgrep (optional dependency)

- URL: https://github.com/BurntSushi/ripgrep
- Used by: `internal/tools` (search)
- Decision: search uses `rg` when `exec.LookPath` finds it (flags: `-n
  --no-heading --max-columns --max-count`, `-i` unless case-sensitive; exit 1
  = no matches), and a built-in walker otherwise. rg is an optimization, never
  a requirement.
- Checked: 2026-09-06

## systemd user service

- URL: https://www.freedesktop.org/software/systemd/man/latest/systemd.service.html
- Used by: `internal/service`
- Decision: keep the local MCP server and OpenAI tunnel-client as separate
  systemd user services. `miodesk.service` owns the loopback server;
  `miodesk-tunnel.service` runs the externally-owned tunnel-client profile,
  requires/starts after the server, and restarts independently. Install writes
  + `daemon-reload` but does not implicitly enable either unit.
- Checked: 2026-09-08 (live install/status, forced tunnel crash/restart, and
  ChatGPT-side reconnect verification on Linux)

## Legacy public tunnel adapters

The following implementation references are retained for compatibility with
older configurations and tests. They are not part of the public default setup
or the primary CLI's connection discovery.

### Cloudflare Tunnel (quick tunnel)

- URL: https://developers.cloudflare.com/tunnel/setup/index.md (found via https://developers.cloudflare.com/tunnel/llms.txt)
- Version: current docs, checked 2026-09-06
- Used by: `internal/tunnel` (cloudflare provider)
- Decision: quick tunnel = `cloudflared tunnel --url http://localhost:<port>` — no account, prints a random `https://<rand>.trycloudflare.com` URL which miodesk parses from output. Documented limits: 200 concurrent requests, no SSE. Named tunnels are out of scope for v1.
- Checked: 2026-09-06

### ngrok agent

- URLs: https://ngrok.com/docs/share-localhost/quickstart.md , https://ngrok.com/docs/gateway/agent/config/v3/ (found via https://ngrok.com/docs/llms.txt)
- Version: current docs, checked 2026-09-06
- Used by: `internal/tunnel` (ngrok provider)
- Decision: `ngrok http <port>` requires an authtoken (`ngrok config check` is the availability probe). Public URL comes from the local web interface/API: `web_addr` is documented as "Network address to bind on for serving the local web interface and api" (default 127.0.0.1:4040) — miodesk binds it to a private port via CLI flags mirroring the documented config keys (`--log stdout --log-format=json --web-addr 127.0.0.1:<free>`) and polls `<web_addr>/api/tunnels` for `public_url`.
- Checked: 2026-09-06

### Tailscale Funnel

- URL: https://tailscale.com/docs/reference/tailscale-cli/funnel (found via https://tailscale.com/docs/features/tailscale-funnel)
- Version: CLI syntax ≥1.52, checked 2026-09-07
- Used by: `internal/tunnel` (tailscale provider)
- Decision: `tailscale funnel --bg http://127.0.0.1:<port>`; public URL read from `tailscale funnel status` (plain-text fallback). Funnel listener ports are restricted to 443/8443/10000 and availability (HTTPS enabled, ACLs) varies by tailnet — probe at runtime, never assume. Teardown: `tailscale funnel http://127.0.0.1:<port> off`; if that fails, hint `tailscale funnel reset` (resets everything, so never run it automatically).
- Checked: 2026-09-07

## ChatGPT Apps (plugins / Apps SDK)

- URLs: https://developers.openai.com/llms.txt →
  https://developers.openai.com/plugins/build/chatgpt-ui.md ,
  https://developers.openai.com/plugins/concepts/mcp-server.md
- Version: current docs, checked 2026-09-23
- Used by: `internal/adapter`, `internal/server`, `plugins/miodesk`
- Decision: miodesk exposes native tools and a compact structured `status` result,
  with invocation labels for `status`, `exec_command`, and `write_stdin`. It
  publishes no MCP Apps UI metadata, templates, or resources. All ten tools
  declare their annotations and schemas; `/api/status` and `/api/edits` retain
  richer local JSON diagnostics behind the server's access middleware.
  ChatGPT can associate optional UI with selected tool invocations, but an
  iframe is scoped to its rendered instance and is not a turn-level singleton.
  That protocol observation explains the decision to keep background status
  lookups in the native tool view without an inline panel.
- Checked: 2026-09-24

## MCP Apps extension (historical protocol guidance)

- URLs: https://modelcontextprotocol.io/extensions/apps/overview.md and /build.md
  (spec: github.com/modelcontextprotocol/ext-apps, spec 2026-01-26)
- Version: checked 2026-09-18
- Used by: historical design reference only; miodesk currently has no MCP Apps UI
- Decision: the extension permits optional tool `_meta.ui.resourceUri` pointing
  to a `ui://` resource, with a sandboxed HTML view and host notifications.
  miodesk currently does not declare such metadata or register UI resources.

## Streamable HTTP transport security (MCP 2026-07-28)

- URL: https://modelcontextprotocol.io/specification/2026-07-28/basic/transports/streamable-http.md
- Version: 2026-07-28, checked 2026-09-07
- Used by: `internal/server` (security.go), `internal/cli` (connect/serve)
- Decision: Servers MUST validate the `Origin` header (403 on invalid) —
  implemented for local and token modes; servers SHOULD bind localhost
  (enforced: non-loopback binds refuse to start unauthenticated); servers
  SHOULD implement authentication. miodesk's model: local (loopback, no
  token) / token (bearer, constant-time compare, token never printed) /
  unsafe (explicit opt-in via `--unsafe-remote` or remote.mode="unsafe",
  Origin checks disabled and documented as part of the risk). OpenAI Secure
  MCP Tunnel is the default remote path; token mode is retained for custom or
  legacy public ingress. The MCP SDK owns Streamable HTTP negotiation; raw
  transport behavior is tested with current protocol headers.
- Checked: 2026-09-07

## ChatGPT connector authentication options

- URL: https://developers.openai.com/plugins/reference.md (securitySchemes),
  https://developers.openai.com/plugins/build/auth.md
- Version: current, checked 2026-09-07
- Used by: `internal/cli` (connect output), `docs/SECURITY.md`
- Decision: ChatGPT's documented connector auth is `noauth` or `oauth2`;
  static bearer tokens are not a connector option. miodesk surfaces this
  honestly: token mode prints the limitation, unsafe mode is the explicit
  (and warned) path for ChatGPT-side testing. OAuth 2.1 / DCR is the future
  proper fix and is tracked as a limitation.
- Checked: 2026-09-07

## OpenAI Secure MCP Tunnel

- URL: https://developers.openai.com/api/docs/guides/secure-mcp-tunnels
- Version: current, checked 2026-09-08
- Used by: deployment guidance and `internal/cli` tunnel output
- Decision: OpenAI's Secure MCP Tunnel is the supported way to connect a
  private developer machine to ChatGPT without exposing a public listener. It
  requires a user-created `tunnel_id`, runtime API key, and workspace/org
  association; `tunnel-client` polls OpenAI and forwards to the local `/mcp`.
  The official guide explicitly requires keeping `tunnel-client run` healthy
  and lists a VM/systemd service as a supported deployment pattern, so Linux
  service installation may supervise tunnel-client as a companion process.
  miodesk still does not parse the profile or copy runtime key material.

## Structured diagnostics

- URL: https://pkg.go.dev/log/slog
- Used by: `internal/logging`, `internal/server`, `internal/cli`
- Decision: use the Go standard structured logger; default to stderr so the
  systemd user service collects records in journald. Request IDs correlate HTTP
  records with MCP tool records; payloads, command lines, Authorization values,
  and bearer tokens are excluded or redacted.
- Checked: 2026-09-07

## Later milestones (not yet consulted — re-verify at implementation time)
