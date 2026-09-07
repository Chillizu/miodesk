# AGENTS.md — miodesk

Status: milestones 1–3 implemented — all seven file/command tools, diff viewer, token stats, systemd service, new-device `setup`, OpenAI Secure MCP Tunnel as the default connection, custom endpoint compatibility, `tunnel list/doctor`, self-update via release manifest (`update --from`), and `scripts/build-release.sh` packaging. ChatGPT/MCP-Apps adaptation is in `internal/adapter`: the diagnostics/status tool and long-command lifecycle opt into the shared widget resource (`ui://miodesk/status-v4.html`) via `_meta.ui` + ChatGPT aliases, while regular file and short-command tools stay Native-first; tool results carry a `kind` discriminator, and the widget renders context-aware per-kind results (read/search/list/write/edit+diff/delete/command lifecycle/status) in a ChatGPT-native visual language — the `/preview` route shows all renderers with mocks. Remote access has a three-tier trust model (`internal/server/security.go`: local / token / unsafe; OpenAI Tunnel keeps the local server on loopback, while token/unsafe modes remain for custom or legacy public entrances; Origin validation follows the MCP transport spec). Remaining polish: full MCP Apps `ui/initialize` bridge (Claude/VS Code hosting), OAuth 2.1 for custom ChatGPT connectors, Windows service/paths. Tunnel-client commands and ChatGPT field names are documented from official sources in docs/REFERENCES.md — re-verify there before changing provider commands or widget meta.

## What this is

`miodesk` is a **local AI / MCP tool bridge** written in Go: it lets ChatGPT, MCP clients, IDE agents, and coding agents safely access the user's workspace, files, and dev tools. Goals: small · fast · elegant · portable · predictable. Single static binary, widget UI embedded via `go:embed`, zero Node/Python/Rust runtime at release, not bound to any single AI client.

Design philosophy: prefer "slightly fewer features but clear" over "more features but complex"; prefer simple direct code over clever abstraction (no interfaces/abstractions until there are ≥2 real implementations); if a feature needs lots of host-specific workarounds, redesign it instead of stacking patches.

## Naming (hard rule)

Always lowercase **`miodesk`** — project name, binary, CLI, config dir, docs. Never `MioDesk`, `MIODesk`, `Mio Desk`, or `mio`. (The workspace folder `MioDesk/` is a pre-existing exception; do not propagate it.)

## Stack rules

- **Go** for everything: CLI, server, MCP, tools, workspace security, config, tunnel orchestration, service mgmt, update.
- **Widget**: plain HTML + CSS + minimal vanilla JS/TS under `web/widget/`, embedded with `go:embed`. No React/Vue/Next, no npm/pnpm in the release path.
- Widget runs inside iframes/WebViews/host windows where **viewport width is meaningless** — use CSS **Container Queries**, custom properties, `ResizeObserver`; never `@media(max-width: ...)` as the main responsive mechanism. Design tokens (`--miodesk-bg`, `--miodesk-accent`, `--miodesk-radius`, …) live centrally; no scattered magic numbers. Restrained animation (opacity/transform). Theme: auto/light/dark, respect host/system; don't assume dark.
- Keep dependencies minimal; prefer stdlib, but don't hand-roll what stdlib already provides. Performance budgets: binary < 20 MB, idle RSS < 30 MB, near-instant startup.

## Architecture boundaries

Intended layout (use something simpler if it's genuinely better):

```
cmd/miodesk/
internal/   xdg, config, workspace, tools, server, doctor, service, cli, buildinfo
web/widget/
docs/
```

- **Transports** (stdio, Streamable HTTP) ≠ **adapters** (mcp, chatgpt, future) ≠ **core tools** (read, search, list, write, edit, delete, command). Tools must not know about ChatGPT-specific metadata; host workarounds stay in the host's adapter.
- **Tunnel is pluggable**: OpenAI Secure MCP Tunnel is the default remote path; `custom` remains available for an operator-owned HTTPS endpoint, and older provider values remain readable for compatibility. Core never hard-depends on a provider and never contains provider-specific URLs/params. `--provider custom --url ...` means miodesk does NOT own/build the public endpoint.
- **Workspace is the security boundary.** All file tools are sandboxed to the workspace root. Validate on canonical/resolved paths (`filepath.EvalSymlinks`) — never string-prefix checks. Must block: `../` escape, absolute-path escape, symlink escape, writes outside workspace. Large ops need limit/offset/depth/truncation.
- **`command` tool**: cwd defaults to workspace; never sudo, never auto-escalate, never install deps silently; return stdout/stderr/exit code/elapsed. Long-running commands use a start/poll/cancel lifecycle — no HTTP request hangs forever. `edit` batches are atomic: any failed op ⇒ zero partial writes.
- **Config**: XDG dirs (`$XDG_CONFIG_HOME/miodesk`, data/state/cache likewise; fallback `~/.config/miodesk` etc.), file `config.toml`. Runtime state never goes into config.toml.

## CLI contract

Subcommands: `setup`, `init`, `serve`, `connect`, `status`, `doctor`, `config`, `workspace`, `tunnel`, `service`, `logs`, `update`, `version`. Fresh `setup` defaults to a loopback server plus OpenAI Secure MCP Tunnel; it does not install dependencies or start daemons silently. Style: fast, quiet, predictable. `[OK] / [WARN] / [INFO]` prefixes. Errors explain what/why/how to fix, e.g.:

```
Error: port 8787 is already in use
Hint: use `miodesk serve --port 0`
```

`--json` machine-readable output required for `doctor` and `status`. `init` must not perform large irreversible changes.

## Build / verify

Standard Go toolchain (no Makefile yet):

```sh
gofmt -w $(git ls-files '*.go')
go vet -all ./...
go test -vet=all ./...
go build -buildvcs=false ./cmd/miodesk
scripts/release-check.sh
```

Definition of done: implemented + formatted + tested + **actually executed** — CLI really run, server really started, a request really made, clean exit. "The code looks correct" is not done.

## Testing priorities

Security paths must be automated tests, never manual-only: `../../../etc/passwd`, symlink escape, command cwd, unsafe/recursive delete, unexpected absolute paths. Also: config parsing, XDG paths, read/list limits, edit atomicity, server start/stop, occupied port, MCP transport, tunnel detection, custom endpoint, CLI smoke tests, diff parser, widget responsive behavior.

## Docs protocol

Never implement protocol details from model memory or old blog posts. Check official docs first: official spec → official SDK → product docs → examples → third-party; prefer llms.txt/markdown; read only the minimal scope needed.

- MCP spec (2026-07-28): https://modelcontextprotocol.io/specification/2026-07-28/index.md — index: https://modelcontextprotocol.io/llms.txt
- Official Go MCP SDK — use it, don't re-implement JSON-RPC/MCP: https://github.com/modelcontextprotocol/go-sdk
- OpenAI/ChatGPT MCP & UI: https://developers.openai.com/llms.txt
- OpenAI Secure MCP Tunnel: https://developers.openai.com/api/docs/guides/secure-mcp-tunnels (default remote path; keep the local MCP server on loopback)
- XDG base dirs: https://specifications.freedesktop.org/basedir-spec/latest/

Record confirmed findings (topic, official URL, version, relevant module, decision, checked date) in `docs/REFERENCES.md` so later agents don't re-search.

## Working style

High autonomy: inspect → decide → implement → test → verify → continue. Stop only for major architectural ambiguity, irreversible decisions, credentials, or security-sensitive operations. Build in milestones: (1) runnable MVP — config + XDG + `version`/`init`/`doctor`/`serve` + workspace sandbox + MCP server + read/search/list + minimal embedded widget + tests; (2) write/edit/delete/command + long-command lifecycle + diff viewer + token stats + service mgmt; (3) `connect` + tunnel providers + packaging + update + cross-platform polish.
