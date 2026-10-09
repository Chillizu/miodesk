# Security Model

This document describes miodesk's real, implemented security boundaries.
For implementation references, see `docs/REFERENCES.md`.

## Workspace sandbox

All file tools (`read`, `search`, `list`, `write`, `edit`, `delete`) are
confined to the configured `workspace.root`. Paths are validated on
canonical, symlink-resolved locations — never on string prefixes — so `../`
traversal, absolute-path escapes, and symlinks pointing outside the workspace
are all rejected. `delete` refuses the workspace root itself. Large operations
are bounded (depth, entry count, byte caps).

## Command tools

`exec_command` and `write_stdin` run with the current user's operating-system
permissions. The command's working directory is resolved inside the configured
workspace (or a workspace-contained subdirectory), but that only constrains
`cwd`; commands are not confined by an OS filesystem sandbox and can access
other resources permitted to the account. A command that outlives the initial
yield window returns an explicit numeric `session_id`; the process remains
subject to the same bounded runtime, output cap, and 32-session concurrency
limit. Pipe mode is the default; `tty=true` creates an interactive Unix PTY.
Common privilege-escalation commands (`sudo`, `doas`, `su`, `pkexec`,
`runuser`, and `runas`) are conservatively refused. That refusal is a
guardrail, not an account sandbox — treat every MCP client you connect as a
full agent on this machine.

## Trust levels

| mode (`[remote]` in config.toml) | who can reach the tools | when to use |
| -------------------------------- | ----------------------- | ----------- |
| `""` / `local` (default)         | local processes only    | stdio clients and local MCP hosts |
| `token`                          | anyone presenting the bearer token | custom or legacy public ingress |
| `unsafe`                         | anyone with the URL     | short-lived debugging, explicitly requested |

### Local trusted access

`miodesk serve` binds `127.0.0.1` by default and requires no token — local
usage stays zero-config. Binding a non-loopback interface without
authentication is refused, and the browser `Origin` header is validated on
every request (the DNS-rebinding defense the MCP Streamable HTTP spec
requires), so web pages cannot reach the local server from a victim's
browser.

### OpenAI Secure MCP Tunnel

The default `miodesk connect` path is OpenAI Secure MCP Tunnel. It keeps the
server on loopback and lets the official local `tunnel-client` make an outbound
HTTPS connection. This is not a public listener on the configured local port;
the tunnel ID, runtime key, workspace association, and control-plane policy
are managed by OpenAI and the external client. See `docs/SETUP.md` for the
setup sequence.

### Remote authenticated access

An operator-owned public ingress, such as `miodesk connect --provider custom`,
is a remote entrance and requires token authentication by default: a 256-bit
token is generated automatically, stored in `config.toml`, and printed once.
Clients must send `Authorization: Bearer <token>` on every request. If
`[remote].mode` is explicitly set to `"local"`, connect refuses to open a
remote entrance at all.

Note: ChatGPT connectors do not currently accept static bearer tokens (their
documented options are `noauth` and `oauth2`). For a ChatGPT connection use
OpenAI Secure MCP Tunnel, or configure OAuth/no-auth explicitly on a
user-managed HTTPS ingress. See unsafe mode below only for short local tests.

### Unsafe development mode

`miodesk connect --unsafe-remote` (or `remote.mode = "unsafe"`) disables all
authentication and Origin checks. The CLI prints a prominent warning and the
access mode. It exists for short-lived debugging with clients that cannot
send headers. Never combine it with a long-lived tunnel.

## Token handling

- The token lives in `config.toml` (user-only file under the XDG config
  directory).
- It is never included in `/api/status`, `miodesk status`, `miodesk doctor`,
  error messages, or debug output — enforced by tests.
- Requests are compared with a constant-time comparison.
- `/healthz` is the only unauthenticated endpoint and returns no data.

## Data surfaces

`/api/status` does not contain bearer tokens or edited file text; it exposes
local diagnostics such as workspace paths and usage counters.
`/api/edits` can contain edited workspace text. It is retained in memory only
(up to 10 records and 2 MiB of diff data) and is controlled by the configured
access mode: local/origin validation, bearer-token authentication, or the
explicitly unsafe mode. MCP tool results can also contain workspace content
requested by the client, such as file reads or command output. When a tunnel is
active, every reachable surface follows the active trust level.

## Logging

The server uses Go's structured `log/slog` logger. HTTP requests, authentication
denials, MCP tool completion/errors, and panics include a short request ID and
elapsed time. The default stderr sink is collected by the systemd user journal;
set `[logging].format = "json"` for machine-readable records or
`MIODESK_LOG=debug` for health checks and other verbose diagnostics.

HTTP request logs contain metadata, not request bodies. MCP tool logging omits
raw arguments and result payloads; failure entries can still include an error
summary. Logs do not include file contents, command lines, bearer tokens, or
Authorization headers. `miodesk logs` also redacts the configured bearer token
defensively before displaying journal output.

## Reporting

If you believe you found a security issue in miodesk, please open an issue
with reproduction steps and keep exploit details minimal until acknowledged.
