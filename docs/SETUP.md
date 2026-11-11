# Device setup

**English** · [简体中文](SETUP.zh-CN.md) · [Guided setup for AI assistants](AGENT_SETUP.md)

This is the path for a new device. It keeps the local server private and
makes the OpenAI connection explicit, so a copied repository does not need
machine-specific paths or hidden background processes.

## 1. Install miodesk

Use a tagged release binary when possible. Linux/macOS users can run the
[checksum-verifying shell installer](../scripts/install.sh), and Windows x64
users the [PowerShell installer](../scripts/install.ps1) after reviewing its
source. The Windows installer doesn't change PATH or provision services;
use the full binary path it prints. Check the [release assets](https://github.com/Chillizu/miodesk/releases) for platform coverage.
Put the binary somewhere on `PATH` (or use the full path) and verify it:

```sh
miodesk version
```

For a source install, use Go 1.26.6 or newer:

```sh
go install github.com/Chillizu/miodesk/cmd/miodesk@latest
```

The release binary has no Node, Python, Rust, or npm dependency. OpenAI
connection support additionally needs the official `tunnel-client`; install
it using the instructions in the [OpenAI Secure MCP Tunnel guide](https://developers.openai.com/api/docs/guides/secure-mcp-tunnels).

## 2. Configure a local workspace

Run setup from the project directory that should be available to the agent:

```sh
cd /absolute/path/to/project
miodesk setup
miodesk doctor
```

The first setup records the current directory as a canonical absolute
workspace root, uses `127.0.0.1:8787`, and defaults to **local-only MCP**.
For guided setup, prefer `miodesk setup --workspace /absolute/known/project`
rather than guessing from the current directory. Since **v0.2.3** you can
also use `setup --non-interactive --workspace ...` and read-only
`preflight --json`. On v0.2.2, use `setup --workspace`.
OpenAI Secure MCP Tunnel is an optional additional client. Setup creates
only miodesk configuration by default: no package installs, workspace edits,
services, or network listeners.

When run from a terminal, `miodesk setup` without arguments opens an
interactive wizard for these values. `miodesk setup --interactive` requests
the same wizard explicitly. The explicit flag form below remains the stable
choice for scripts and CI, and non-interactive stdin is never left waiting for
input.

To configure a different local port, use a fixed port:

```sh
miodesk setup --workspace /absolute/path/to/project --port 9900
```

The port is local only. It is the target that `tunnel-client` forwards to; it
is not a public port. Port `0` is useful for an ephemeral local test but is
not valid for an OpenAI tunnel profile.

`setup` is idempotent and changes only Core configuration unless you explicitly
supply tunnel flags. Existing OpenAI profiles are **not** silently rebuilt.
If you change the Core port, explicitly update the tunnel target afterward:

```sh
miodesk setup --port 9900
miodesk tunnel setup
# Then restart the respective Core and tunnel processes during maintenance.
```

The `--force` flag is only needed when changing the workspace root in an
existing configuration.

## 3. Configure OpenAI Secure MCP Tunnel

OpenAI Platform provides the tunnel ID and runtime API key. The tunnel must be
associated with the intended ChatGPT workspace and Platform organization as
described by OpenAI's guide. For the simplest first-time path, follow the
[one-block, OS-specific interactive credential setup](AGENT_SETUP.md#chatgpt-only-stage-official-secure-mcp-tunnel): it prompts for Tunnel ID,
reads the runtime key without echo, saves it locally with private permissions,
and calls the existing setup command. No credentials need to be pasted into
ChatGPT. The **original** `miodesk setup --interactive` wizard remains
supported for users who prefer it (it asks for the key-file **path**, never
the secret itself).

If you already created a private key file, on Unix systems:

```sh
chmod 600 /absolute/path/to/openai-runtime-key
```

Then run:

```sh
miodesk setup \
  --workspace /absolute/path/to/project \
  --tunnel-id tunnel_… \
  --runtime-key-file /absolute/path/to/openai-runtime-key
```

This command:

1. validates the fixed local port, tunnel ID, key-file type, and key-file
   permissions;
2. stores only non-secret paths and the tunnel ID in
   `~/.config/miodesk/config.toml` (or the XDG equivalent);
3. asks `tunnel-client` to create the `miodesk` profile in
   `~/.config/tunnel-client/miodesk.yaml` (or the XDG equivalent); and
4. keeps both the key contents and the external client's output out of the
   miodesk output.

The exact default paths can be changed with `--runtime-key-file`,
`--profile-dir`, `--profile`, and `--tunnel-client`. Absolute paths are
recommended for a user service. The generated profile is owned by
`tunnel-client`; miodesk does not reimplement or parse its YAML format.

Check the complete local configuration before connecting:

```sh
miodesk doctor
tunnel-client doctor --profile miodesk --explain
```

If `tunnel-client` is not installed, or if the Platform tunnel is not ready,
`miodesk doctor` reports the missing prerequisite and does not expose a
fallback public endpoint automatically.

## 4. Start the connection

For a foreground run, one command starts the local MCP server and the OpenAI
tunnel together:

```sh
miodesk connect
```

Press `Ctrl-C` to stop both processes. The local endpoint is normally
`http://127.0.0.1:8787/mcp`; OpenAI receives the connection through the
outbound tunnel, not through a public listener on that port.

**First verify the foreground connection and a real ChatGPT `status` tool
call before offering persistent services.** The following is an optional
post-connection step requiring the user's decision. On Linux, install and
start Core **independently**, allowing multiple local clients to share its
MCP endpoint:

```sh
miodesk service install
miodesk service start
```

For a configured OpenAI profile, separately install/start the optional tunnel:

```sh
miodesk tunnel service install
miodesk tunnel service start
```

The tunnel is an external `tunnel-client` process, not another copy of Core.
The generated `miodesk-tunnel.service` has only startup ordering (`After`),
not a hard `Requires=miodesk.service` dependency. A restart/stop of one unit
does not issue commands against the other. Both can be enabled independently
at login:

```sh
systemctl --user enable miodesk
systemctl --user enable miodesk-tunnel
```

To migrate from a previously installed combined unit, inspect the **actual**
unit (`systemctl --user cat miodesk-tunnel.service`) for `Requires=miodesk.service`,
`Wants=miodesk.service` or `PartOf=miodesk.service` dependencies. An existing
custom unit may also use `EnvironmentFile=` to supply authentication. Verify
that the tunnel-client profile and key-file reference can work without that
environment file **before** rewriting the unit with
`miodesk tunnel service install`; schedule a controlled tunnel restart and
ChatGPT reconnect test afterward. If an existing unit has `EnvironmentFile=`,
the installer **refuses to overwrite it** to prevent credential loss. Preserve
that unit and use a reviewed systemd drop-in to clear **only** the Core
lifecycle coupling (for example, for a unit with `PartOf=miodesk.service` and
`Wants=network-online.target miodesk.service`):

```ini
# ~/.config/systemd/user/miodesk-tunnel.service.d/independent.conf
[Unit]
PartOf=
Wants=
Wants=network-online.target
```

Run `systemctl --user daemon-reload`, inspect `systemctl --user show
miodesk-tunnel.service -p PartOf -p Wants -p Requires`, and only then run a
controlled Core restart to prove the tunnel PID remains unchanged. Review any
other original dependency lines first; an override can remove important
custom dependencies. Upgrading Core alone does not migrate units. The
legacy `miodesk connect` foreground mode still starts both when explicitly
requested, but should not be used while another tunnel-client is running.

On macOS and Windows, use the foreground command or the host's own process
manager. miodesk does not silently install an operating-system service.

## 5. Add it to ChatGPT

With Secure MCP Tunnel, select the matching Tunnel in ChatGPT Developer
Mode/connector settings. Do not paste the local `127.0.0.1` URL into ChatGPT;
the host cannot reach another device's loopback interface. The OpenAI tunnel
flow also does not require choosing miodesk's static bearer-token mode.

If you instead bring your own public HTTPS reverse proxy, use the advanced
custom endpoint mode:

```sh
miodesk connect --provider custom --url https://mcp.example.com
```

That mode does not create or secure the proxy. The operator must route `/mcp`
to the local service and provide connector-compatible authentication. ChatGPT
connectors document OAuth 2.1 and explicit no-auth testing; a miodesk bearer
token is not a documented ChatGPT connector credential.

## Local panel and private Tunnel diagnostics

Unreleased source builds serve **http://127.0.0.1:8787/panel/** alongside
HTTP MCP, without another miodesk listener. This is not part of v0.2.3.
The panel distinguishes Core availability, mioweave authorization and Tunnel
readiness; none substitutes for a real ChatGPT tool call.

For mioweave, open the connection/token disclosure (`连接与 Token 设置`), import
the existing Bridge token, then point the Bridge service's
`WEBMCP_BRIDGE_TOKEN_FILE` at the displayed private XDG
`miodesk/credentials/mioweave-token` path. Clear any explicit
`WEBMCP_BRIDGE_TOKEN`, which otherwise takes precedence. Restart Bridge once
after changing its environment. File-backed token replacements then reload
without a restart; update the extension after rotation. Panel never overwrites
the old repository token file or an arbitrary selected path.

The official `tunnel-client` can serve its health/admin API over a Unix socket
instead of TCP (confirmed with installed v0.0.14). For an already validated
Linux user service, a credential-preserving drop-in can contain:

```ini
[Service]
RuntimeDirectory=miodesk-tunnel
RuntimeDirectoryMode=0700
UMask=0077
Environment=HEALTH_UNIX_SOCKET=%t/miodesk-tunnel/admin.sock
```

Set `[panel].tunnel_socket` in `miodesk config`'s file to the **absolute**
corresponding path, normally `$XDG_RUNTIME_DIR/miodesk-tunnel/admin.sock`.
TOML does not expand shell variables; use the actual value. Inspect existing
health flags/environment first: explicit CLI flags have precedence. Apply the
drop-in with a controlled daemon reload/Tunnel restart and restart Core to load
the diagnostics reference. Do not replace credentials, the profile or
startup-readiness drop-ins. Confirm the socket directory is private, the old
TCP admin port (such as 10721) is no longer listening, and Panel reports both
health and readiness. Tunnel stays separate and optional; Core tools do not
depend on its admin socket.

Logs select `--source core|tunnel|mioweave|all`, minimum
`--level debug|info|warn|error`, `--since 10m`, and case-insensitive literal
`--grep`. `--json` is normalized NDJSON (`time`, `source`, `level`, `message`,
optional `fields`), not raw journal JSON. `-n 0 --follow` follows new records
only. Foreground output remains in its terminal, not collected by Panel.
Never publish/reverse-proxy Panel.

## Troubleshooting

| symptom | check |
| ------- | ----- |
| missing tunnel ID | `miodesk setup --tunnel-id tunnel_… --runtime-key-file <file>` |
| key rejected | `chmod 600 <file>` and rerun setup |
| client missing | install the official `tunnel-client`, then rerun setup |
| profile missing or stale | rerun `miodesk setup` with the same tunnel and key paths |
| local port unavailable | choose a different fixed `--port`, then rerun setup |
| tunnel control-plane failure | `tunnel-client doctor --profile miodesk --explain` |
| local server failure | `miodesk status`, `miodesk logs --follow --json` |

The server health endpoint is local and contains no workspace data:

```sh
curl --fail http://127.0.0.1:8787/healthz
```

If a service is not installed, foreground logs are written to the terminal.
With the Linux user service, use `miodesk logs`; records include request IDs
but intentionally omit file contents, command lines, authorization headers,
and secrets.

## Existing configurations

Existing configurations are not silently rewritten from another connection
mode. A fresh `miodesk setup` uses local-only MCP by default; an older config is
preserved until you explicitly provide `--tunnel-id` or edit its connection
settings. Legacy provider values remain readable for compatibility, but they
are not part of the primary onboarding path.
