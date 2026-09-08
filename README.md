<p align="center">
  <img src="Miodesk-logo.png" width="180" alt="Miodesk logo">
</p>

<h1 align="center">miodesk</h1>

<p align="center"><strong>A small, fast, portable local AI / MCP tool bridge.</strong></p>

miodesk lets ChatGPT, MCP clients, IDE agents, and coding agents work with a
chosen workspace and its development tools through one static binary. The
workspace is the security boundary: file tools stay inside it, commands run
there by default, and the local server binds to loopback unless you explicitly
choose another access mode.

## Install

For normal use, download the binary for your platform from a tagged GitHub
release and put it on `PATH`. A source install is also available when Go
1.26.6 or newer is installed:

```sh
go install github.com/Chillizu/miodesk/cmd/miodesk@latest
```

No Node, Python, Rust, package manager, or runtime service is required by the
released binary. The optional OpenAI connection uses the separately installed
official `tunnel-client`.

## First run

Run setup from the directory that should be accessible to the agent:

```sh
cd /absolute/path/to/workspace
miodesk setup
miodesk doctor
miodesk serve
```

`setup` is safe to rerun. On a new configuration it records the current
directory as an absolute workspace root, uses local port `8787`, keeps the
server on `127.0.0.1`, and selects [OpenAI Secure MCP Tunnel](https://developers.openai.com/api/docs/guides/secure-mcp-tunnels)
as the default remote path. It does not install software, guess credentials,
start a daemon, or print secrets.

In a real terminal, running `miodesk setup` without arguments opens a small
interactive wizard for the workspace, port, and optional OpenAI tunnel values.
Use `miodesk setup --interactive` to request the wizard explicitly. The
flag-based form remains available for scripts and CI; non-interactive stdin
never waits for prompts.

To choose another local port, use a fixed value and rerun setup:

```sh
miodesk setup --workspace /absolute/path/to/workspace --port 9900
```

Port `0` is reserved for local ephemeral tests. OpenAI tunnel profiles need a
fixed local target so the profile continues to point at the same endpoint.

## Connect ChatGPT

The recommended remote path is OpenAI Secure MCP Tunnel. The MCP server stays
private on loopback; the local `tunnel-client` opens an outbound HTTPS
connection. There is no public URL to copy and no need to expose the local
port.

After creating a tunnel in OpenAI Platform and obtaining its runtime key file:

```sh
miodesk setup \
  --workspace /absolute/path/to/workspace \
  --tunnel-id tunnel_… \
  --runtime-key-file /absolute/path/to/openai-runtime-key
miodesk doctor
miodesk connect
```

The setup command asks `tunnel-client` to create or refresh the `miodesk`
profile. The key is referenced by file and is never copied into
`config.toml`, the repository, or command output. In ChatGPT, select the
corresponding Tunnel in Developer Mode/connector settings. See
[docs/SETUP.md](docs/SETUP.md) and [docs/CHATGPT.md](docs/CHATGPT.md) for the
credential, workspace association, and troubleshooting details.

On Linux, `miodesk service install` installs the local server unit and, when
OpenAI Tunnel is configured, a companion `miodesk-tunnel.service` that runs
`tunnel-client` from the generated profile:

```sh
miodesk service install
miodesk service start
systemctl --user enable miodesk miodesk-tunnel  # optional: start at login/boot
```

`miodesk service start|stop|restart|status|uninstall` manages the pair
together. If the persistent tunnel is already running, `miodesk connect`
reuses it instead of starting a second tunnel client. Without installed
services, `miodesk connect` still starts both processes in the foreground.

## Commands

| command     | purpose                                                      |
| ----------- | ------------------------------------------------------------ |
| `setup`     | configure a new device, workspace, port, and OpenAI profile |
| `init`      | create or update the low-level configuration                 |
| `serve`     | run the local MCP server and widget                          |
| `connect`   | run/reuse the server and connect through the default tunnel  |
| `tunnel`    | inspect the default connection with `list` or `doctor`      |
| `status`    | show local server, tunnel provider, and tunnel service state (`--json`) |
| `doctor`    | check configuration, workspace, connection, and ports       |
| `logs`      | show collected systemd records or foreground guidance       |
| `service`   | manage the Linux server + tunnel systemd user services                  |
| `config`    | print the configuration file path                           |
| `workspace` | print the configured workspace root                         |
| `update`    | verify and atomically apply a release manifest              |
| `version`   | print build information                                     |

For local MCP hosts, stdio avoids networking entirely:

```json
{
  "mcpServers": {
    "miodesk": {
      "command": "/absolute/path/to/miodesk",
      "args": ["serve", "--stdio"]
    }
  }
}
```

## Custom HTTPS endpoint

OpenAI Secure MCP Tunnel is the default and the only connection path shown by
the primary onboarding flow. If an operator already owns a reverse proxy or
another HTTPS ingress, the advanced custom mode is available:

```sh
miodesk connect --provider custom --url https://mcp.example.com
```

This does not create, secure, or verify the public endpoint; it only tells
miodesk which endpoint the operator has prepared. A public connector must use
an authentication mode supported by that connector, normally OAuth 2.1 or an
explicit no-auth test. The static bearer token used by miodesk's legacy remote
mode is not a documented ChatGPT connector option. Keep the custom endpoint
behind a properly configured proxy and use `docs/SECURITY.md` as the checklist.

## Tools

All file tools are sandboxed inside `workspace.root`. Paths are validated on
canonical, symlink-resolved locations — `../`, absolute-path escapes, and
symlink escapes are rejected.

- **read** — bounded text reads with `offset`/`limit` (512 KiB cap per call)
- **search** — text search; ripgrep when installed, built-in engine otherwise
- **list** — directory entries with bounded depth and count
- **write** — create or overwrite; parent directories only with `create_dirs`
- **edit** — validated atomic edits with structured diffs
- **delete** — guarded file, symlink, and recursive-directory deletion
- **command** — bounded command execution with stdout, stderr, exit code, and elapsed time
- **command_start / command_poll / command_cancel** — lifecycle for long tasks

## ChatGPT UI

miodesk is Native-first. Regular file and short-command results remain useful
in the host's native tool view and do not create an extra widget. Only status
and long-running command lifecycle results opt into the optional embedded MCP
Apps widget. The widget is plain embedded HTML/CSS/JavaScript, has no external
origin, supports light/dark/auto themes, and uses container-aware responsive
layout for narrow ChatGPT views. Preview it at `/widget` while a local server
is running.

## Configuration

The configuration is stored at `$XDG_CONFIG_HOME/miodesk/config.toml`, falling
back to `~/.config/miodesk/config.toml`:

```toml
[server]
host = "127.0.0.1"
port = 8787

[workspace]
root = "/absolute/path/to/workspace"

[tunnel]
provider = "openai"

[tunnel.openai]
profile = "miodesk"
# tunnel_id, runtime_key_file, profile_dir, and client_path are optional
# until OpenAI Secure MCP Tunnel is configured.

[widget]
theme = "auto"      # auto | light | dark

[logging]
level = "info"      # debug | info | warn | error
format = "text"     # text | json
```

The runtime key is a file reference, not a TOML secret. Keep the key file
user-readable only (`chmod 600` on Unix). `miodesk doctor` checks the path,
permissions, client, profile, fixed port, and local server without displaying
the key.

## Logs and diagnostics

```sh
miodesk doctor
miodesk tunnel doctor
miodesk logs --follow --json
```

The server emits request IDs and structured events for HTTP requests,
authentication denials, MCP tool completion/errors, startup, and shutdown. It
does not log file contents, command lines, bearer tokens, or Authorization
headers. On Linux, the user service sends these records to journald.

## Development and release

```sh
gofmt -w $(git ls-files '*.go')
go vet -all ./...
go test -vet=all ./...
go test -race ./...
go build -buildvcs=false -o miodesk ./cmd/miodesk
scripts/release-check.sh
scripts/build-release.sh 0.2.1
```

The release script produces platform binaries, `checksums.txt`, and a
`manifest.json` consumable by `miodesk update --from <manifest-url>`. The
release checklist is in [docs/RELEASE.md](docs/RELEASE.md).

See [docs/SECURITY.md](docs/SECURITY.md) for the threat model and
[docs/REFERENCES.md](docs/REFERENCES.md) for the official protocol and
platform sources used by the implementation. The repository is licensed
under [MPL-2.0](LICENSE).
