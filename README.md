# miodesk

A small · fast · elegant · portable · predictable **local AI / MCP tool bridge**.

miodesk lets ChatGPT, MCP clients, IDE agents, and coding agents safely access
your workspace, files, and dev tools — from one static binary with zero runtime
dependencies. Local access is zero-config; remote entrances require token
authentication. See [docs/SECURITY.md](docs/SECURITY.md).

## Quick start

```sh
miodesk init      # create the default config
miodesk serve     # run the local MCP server + widget
miodesk doctor    # check your installation
```

Add to an MCP client (stdio, local):

```json
{ "command": "miodesk", "args": ["serve", "--stdio"] }
```

Or point a remote client at the Streamable HTTP endpoint:

```
http://127.0.0.1:<port>/mcp
```

## Commands

| command     | purpose                                          |
| ----------- | ------------------------------------------------ |
| `init`      | create the default configuration                 |
| `serve`     | run the MCP server (HTTP + widget, or `--stdio`) |
| `connect`   | run the server and expose it via a tunnel        |
| `tunnel`    | `list` / `doctor` for tunnel providers           |
| `status`    | is the local server running? (`--json`)          |
| `logs`      | show server logs from the systemd journal        |
| `doctor`    | check config, workspace, ports, providers        |
| `service`   | manage the systemd user service (Linux)          |
| `config`    | print the config file path                       |
| `workspace` | print the workspace root                         |
| `update`    | check a release feed and replace the binary      |
| `version`   | build information                                |

## Public endpoints (miodesk connect)

`miodesk connect` starts the server and exposes it through a pluggable tunnel
provider. Detection order for `provider = "auto"`: cloudflare → ngrok →
tailscale. A missing binary is never fatal — you get a warning and
alternatives.

```sh
miodesk connect                                     # auto-detect a provider
miodesk connect --provider cloudflare               # quick tunnel (no account)
miodesk connect --provider ngrok                    # requires authtoken
miodesk connect --provider tailscale                # requires Funnel enabled
miodesk connect --provider custom --url https://you.example.com
miodesk tunnel list && miodesk tunnel doctor
```

Inspect recent service records, follow live traffic, or emit structured JSON
for troubleshooting:

```sh
miodesk logs
miodesk logs --follow
miodesk logs --json --since 10m
miodesk logs --grep 'auth_denied'
```

## Tools

All file tools are sandboxed inside the configured `workspace.root`. Paths are
validated on canonical, symlink-resolved locations — `../`, absolute paths,
and symlink escapes are rejected.

- **read** — bounded text reads with `offset`/`limit` (512 KiB cap per call)
- **search** — text search; ripgrep when installed, built-in engine otherwise
- **list** — directory entries with bounded depth and count
- **write** — create or overwrite; parent directories only with `create_dirs`
- **edit** — exact-replace and guarded range edits; every operation is
  validated before anything is written and each file write is atomic
  (temp + rename); returns structured diffs
- **delete** — files, symlinks (unlinked, never followed), and directories
  (non-empty requires `recursive`)
- **command** — short commands with stdout/stderr/exit code/elapsed time;
  sudo is refused
- **command_start / command_poll / command_cancel** — long-running tasks with
  a start/poll/cancel lifecycle, so no request ever hangs

## Widget

`miodesk serve` also serves a small status widget at `http://127.0.0.1:<port>/`:
tool cards with real call counts, a usage strip (`ƒ` tool calls), a diff viewer
for recent edits (change groups, prev/next navigation, closable inline toolbar,
container-query reflow), and light/dark/auto theming — plain HTML/CSS/JS
embedded in the binary via `go:embed`. No Node, no bundler.

### ChatGPT / MCP Apps

miodesk speaks the ChatGPT Apps UI conventions and the MCP Apps extension out
of the box. A read-only `status` tool returns the dashboard payload and
declares its UI via `_meta.ui.resourceUri` (`ui://miodesk/status.html`, with
the `openai/outputTemplate` compatibility alias); hosts fetch the
`text/html;profile=mcp-app` resource — a fully self-contained page (no
external origins, `prefersBorder: false`) that renders from the tool's
`structuredContent` via `window.openai.toolOutput` or the
`ui/notifications/tool-result` notification. All 11 tools carry explicit
annotations (`readOnlyHint`/`destructiveHint`/`openWorldHint`), titles, and
invocation status labels; the server declares instructions for model guidance.
Preview the hosted widget at `http://127.0.0.1:<port>/widget`.

For ChatGPT connector setup and the security trade-offs, see
[docs/CHATGPT.md](docs/CHATGPT.md). In short: ChatGPT needs a reachable HTTPS
MCP endpoint; production deployments should use OpenAI's Secure MCP Tunnel or
an OAuth 2.1-compatible authentication server. The static bearer token emitted
by `miodesk connect` is intended for clients that can send an
`Authorization` header and is not a documented ChatGPT connector option.

The server emits request IDs and structured events for HTTP requests,
authentication denials, MCP tool completion/errors, startup, and shutdown. It
never logs file contents, command lines, bearer tokens, or Authorization
headers. With the systemd user service, records go to the user journal.

## Configuration

`config.toml` lives in `$XDG_CONFIG_HOME/miodesk` (fallback `~/.config/miodesk`):

```toml
[server]
host = "127.0.0.1"
port = 0            # 0 = random free port on each start

[workspace]
root = "/home/you"

[tunnel]
provider = "auto"   # auto | local | cloudflare | ngrok | tailscale | custom

[widget]
theme = "auto"      # auto | light | dark

[logging]
level = "info"       # debug | info | warn | error
format = "text"      # text | json; JSON is useful for journal queries
```

## Development

```sh
gofmt -w .
go vet ./...
go test ./...
go build -buildvcs=false -o miodesk ./cmd/miodesk
scripts/build-release.sh 0.3.0   # cross-compile dist/ + checksums
```

Releases are plain binaries plus a JSON manifest
(`{"version": "...", "assets": {"linux/amd64": {"url": "...", "sha256": "..."}}}`),
which `miodesk update --from <manifest-url>` consumes for safe self-updates.

See [AGENTS.md](AGENTS.md) for architecture rules and
[docs/REFERENCES.md](docs/REFERENCES.md) for the official documentation used
by each module.
