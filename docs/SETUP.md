# Device setup

This is the path for a new device. It keeps the local server private and
makes the OpenAI connection explicit, so a copied repository does not need
machine-specific paths or hidden background processes.

## 1. Install miodesk

Use a tagged release binary when possible. Put it somewhere on `PATH` and
verify it:

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
workspace root, uses `127.0.0.1:8787`, and selects OpenAI Secure MCP Tunnel as
the default remote path. It creates only miodesk configuration. It does not
install packages, change files in the workspace, install a service, or start a
network listener.

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

`setup` is idempotent. Re-running it refreshes the OpenAI profile when OpenAI
credentials have been configured. The `--force` flag is only needed when
changing the workspace root in an existing configuration.

## 3. Configure OpenAI Secure MCP Tunnel

OpenAI Platform provides the tunnel ID and runtime API key. The tunnel must be
associated with the intended ChatGPT workspace and Platform organization as
described by OpenAI's guide. Save the runtime key in a user-only file. On Unix
systems:

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

On Linux, the server can be kept alive by the systemd user service:

```sh
miodesk service install
miodesk service start
miodesk connect
```

When the service is already running, `miodesk connect` reuses it and keeps
only the tunnel client in the foreground. `service install` does not enable
the unit implicitly; enable it after checking the setup with:

```sh
systemctl --user enable miodesk
```

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
mode. A fresh `miodesk setup` uses OpenAI by default; an older config is
preserved until you explicitly provide `--tunnel-id` or edit its connection
settings. Legacy provider values remain readable for compatibility, but they
are not part of the primary onboarding path.
