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

## Command tool

`command` and `command_start` run inside the workspace with the workspace (or
a sandboxed subdirectory) as cwd. Commands containing `sudo` as a standalone
token are conservatively refused. Output is capped. This is a guardrail, not
an account sandbox: commands can still do anything the user's account can do —
treat every MCP client you connect as a full agent on this machine.

## Trust levels

| mode (`[remote]` in config.toml) | who can reach the tools | when to use |
| -------------------------------- | ----------------------- | ----------- |
| `""` / `local` (default)         | local processes only    | stdio clients, local MCP hosts, the local widget |
| `token`                          | anyone presenting the bearer token | remote access via `miodesk connect` |
| `unsafe`                         | anyone with the URL     | short-lived debugging, explicitly requested |

### Local trusted access

`miodesk serve` binds `127.0.0.1` by default and requires no token — local
usage stays zero-config. Binding a non-loopback interface without
authentication is refused, and the browser `Origin` header is validated on
every request (the DNS-rebinding defense the MCP Streamable HTTP spec
requires), so web pages cannot reach the local server from a victim's
browser.

### Remote authenticated access

`miodesk connect` opens a public entrance by definition, so it requires
token authentication: a 256-bit token is generated automatically, stored in
`config.toml`, and printed once. Clients must send
`Authorization: Bearer <token>` on every request. If `[remote].mode` is
explicitly set to `"local"`, connect refuses to open a remote entrance at all.

Note: ChatGPT connectors do not currently accept static bearer tokens (their
documented options are `noauth` and `oauth2`). For ChatGPT-specific testing,
see unsafe mode below; prefer OAuth-based flows for real deployments
(or OpenAI Secure MCP Tunnel, which keeps the server private).

### Unsafe development mode

`miodesk connect --unsafe-remote` (or `remote.mode = "unsafe"`) disables all
authentication and Origin checks. The CLI prints a prominent warning and the
access mode. It exists for short-lived debugging with clients that cannot
send headers. Never combine it with a long-lived tunnel.

## Token handling

- The token lives in `config.toml` (user-only file under the XDG config
  directory).
- It is never included in `/api/status`, `miodesk status`, `miodesk doctor`,
  the widget, error messages, or debug output — enforced by tests.
- Requests are compared with a constant-time comparison.
- `/healthz` is the only unauthenticated endpoint and returns no data.

## Data surfaces

`/api/status`, `/api/edits`, `/widget`, and the MCP endpoint all carry no
secrets; they expose workspace paths and usage counters only. When a tunnel is
active, everything reachable is gated by the active trust level.

## Logging

The server uses Go's structured `log/slog` logger. HTTP requests, authentication
denials, MCP tool completion/errors, and panics include a short request ID and
elapsed time. The default stderr sink is collected by the systemd user journal;
set `[logging].format = "json"` for machine-readable records or
`MIODESK_LOG=debug` for health checks and other verbose diagnostics.

Logging intentionally omits file contents, command lines, bearer tokens,
Authorization headers, and tool arguments. `miodesk logs` also redacts the
configured bearer token defensively before displaying journal output.

## Reporting

If you believe you found a security issue in miodesk, please open an issue
with reproduction steps and keep exploit details minimal until acknowledged.
