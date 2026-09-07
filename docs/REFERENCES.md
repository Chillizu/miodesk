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
  stateless direction, SEP-2567); miodesk does not hand-roll session IDs or
  SSE. Tool input schemas come from Go struct tags via the SDK. Current
  Streamable HTTP protocol headers are left to the SDK.
- Checked: 2026-09-07 (SDK source and current spec)

## OpenAI / ChatGPT MCP

- URL: https://developers.openai.com/llms.txt
- ChatGPT UI: https://developers.openai.com/plugins/build/chatgpt-ui
- Used by: `internal/adapter`, `internal/cli` deployment guidance
- Decision: ChatGPT-specific metadata and widget resources stay in the adapter
  layer; core tools and transports stay host-agnostic. ChatGPT connectors
  document `noauth` and OAuth 2.1-style flows; a static bearer token is not a
  documented connector authentication choice.
- Checked: 2026-09-07

Public onboarding intentionally exposes OpenAI Secure MCP Tunnel as the
default connection. The legacy provider implementations listed below remain
for compatibility and tests; they are not enumerated by the primary setup
flow.

## XDG Base Directory Specification

- URL: https://specifications.freedesktop.org/basedir-spec/latest/
- Used by: `internal/xdg`, `internal/config`
- Decision: `$XDG_{CONFIG,DATA,STATE,CACHE}_HOME/miodesk` with `~/.config`,
  `~/.local/share`, `~/.local/state`, `~/.cache` fallbacks; relative
  `$XDG_*` values are rejected. Runtime state (server.json) goes to the state
  dir, never into config.toml.
- Checked: 2026-09-06

## go:embed

- URL: https://pkg.go.dev/embed
- Used by: `web/widget`
- Decision: widget assets (HTML/CSS/JS) are embedded with `//go:embed static`
  and served via `fs.Sub` + `http.FileServerFS`. No Node/npm build step.
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
- Decision: minimal unit only — Description, After, ExecStart, Restart,
  RestartSec, WantedBy=default.target. No type/specifier tricks; miodesk owns
  config/port/logs. Unit lives at $XDG_CONFIG_HOME/systemd/user/miodesk.service;
  install writes + `daemon-reload` (no implicit enable). Re-verify only if
  specifiers (%h etc.) are ever added.
- Checked: 2026-09-06 (live install/status/uninstall on linux)

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

- URLs: https://developers.openai.com/llms.txt → https://developers.openai.com/plugins/llms.txt →
  build/chatgpt-ui.md , plugins/reference.md , build/app-guidelines.md (all `.md` fetchable)
- Version: current docs, checked 2026-09-07
- Used by: `internal/adapter`, `internal/server`
- Decision:
  - UI tools declare the shared MCP Apps field `_meta.ui.resourceUri`
    (`ui://…`); `openai/outputTemplate` is set as the documented compatibility
    alias. `openai/toolInvocation/invoking|invoked` labels are ≤64 chars.
  - The widget resource uses mimeType `text/html;profile=mcp-app` and carries
    `_meta.ui` (`prefersBorder`; `csp`/`domain` omitted — the asset is fully
    self-contained with zero external origins).
  - ChatGPT reads `annotations` (readOnlyHint/destructiveHint/openWorldHint —
    treated as required) and `title`; every miodesk tool sets them explicitly.
  - The embedded widget reads data from `window.openai.toolOutput` (documented
    alias) or the `ui/notifications/tool-result` postMessage notification, and
    falls back to fetching `/api/status` when opened standalone. It also
    performs the dependency-free MCP Apps `ui/initialize` handshake and
    applies host theme-change notifications.
  - Structured output tools declare `outputSchema`.
- Checked: 2026-09-07

## MCP Apps extension

- URLs: https://modelcontextprotocol.io/extensions/apps/overview.md and /build.md
  (spec: github.com/modelcontextprotocol/ext-apps, spec 2026-01-26)
- Version: current, checked 2026-09-07
- Used by: `internal/adapter`
- Decision: declare UI via tool `_meta.ui.resourceUri` → `ui://` resource;
  keep the widget sandbox-friendly (no external loads, DOM-API-only
  rendering). The embedded widget implements the stable 2026-01-26
  `ui/initialize`/`ui/notifications/initialized` handshake and standard
  tool-result/host-context notifications without adding a JavaScript runtime.
- Checked: 2026-09-07

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
- Version: current, checked 2026-09-07
- Used by: deployment guidance and `internal/cli` tunnel output
- Decision: OpenAI's Secure MCP Tunnel is the supported way to connect a
  private developer machine to ChatGPT without exposing a public listener. It
  requires a user-created `tunnel_id`, runtime API key, and workspace/org
  association; `tunnel-client` polls OpenAI and forwards to the local `/mcp`.
  miodesk does not invent or store those credentials. A public HTTPS endpoint
  still needs ChatGPT-compatible `noauth` or OAuth; miodesk's static bearer
  mode is for clients that can send an Authorization header.

## Structured diagnostics

- URL: https://pkg.go.dev/log/slog
- Used by: `internal/logging`, `internal/server`, `internal/cli`
- Decision: use the Go standard structured logger; default to stderr so the
  systemd user service collects records in journald. Request IDs correlate HTTP
  records with MCP tool records; payloads, command lines, Authorization values,
  and bearer tokens are excluded or redacted.
- Checked: 2026-09-07

## Later milestones (not yet consulted — re-verify at implementation time)
