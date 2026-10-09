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

On Linux and macOS, the recommended installer downloads the matching release
binary, verifies it against the release SHA-256 checksums, and installs it to
`~/.local/bin`:

```sh
curl -fsSL https://raw.githubusercontent.com/Chillizu/miodesk/main/scripts/install.sh | sh
```

To inspect the installer before running it:

```sh
curl -fsSLo miodesk-install.sh https://raw.githubusercontent.com/Chillizu/miodesk/main/scripts/install.sh
less miodesk-install.sh
sh miodesk-install.sh
```

Useful installer options:

```sh
sh miodesk-install.sh --version 0.2.1
sh miodesk-install.sh --bin-dir ~/bin
```

The installer only places the verified binary on disk. It does not configure a
workspace, write credentials, install a service, or start a daemon.

Windows users can download `miodesk-windows-amd64.exe` directly from a tagged
GitHub release. On any supported platform, you can also install from source
when Go 1.26.6 or newer is available:

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
server on `127.0.0.1`, and defaults to local-only MCP. [OpenAI Secure MCP Tunnel](https://developers.openai.com/api/docs/guides/secure-mcp-tunnels)
is an optional client configured separately. Setup does not install software, guess credentials,
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
fixed local target. Changing the Core port does not silently update an existing
tunnel-client profile: run `miodesk tunnel setup` explicitly afterward.

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

On Linux, the local MCP service and the optional OpenAI tunnel each have their
own independently managed systemd user unit. For local-only / MioWeave use:

```sh
miodesk service install
miodesk service start
systemctl --user enable miodesk  # optional
```

After configuring the OpenAI profile, install and start the **separate**
tunnel unit when ChatGPT access is needed:

```sh
miodesk tunnel service install
miodesk tunnel service start
systemctl --user enable miodesk-tunnel  # optional
```

`miodesk service start|stop|restart|uninstall` operates **only** on Core;
`miodesk tunnel service <verb>` operates **only** on the tunnel. Neither
requires or silently restarts the other. The older `miodesk connect` combined
foreground command remains available for compatibility, but is not needed for
two independent systemd units. Some older/custom installations have tunnel unit relationships like
`PartOf=miodesk.service`, `Wants=miodesk.service`, or `Requires=miodesk.service`.
Inspect the **installed** `miodesk-tunnel.service` before migration. If any of
these are present, validate your tunnel-client profile and runtime-key reference,
then explicitly run `miodesk tunnel service install` and schedule a controlled
tunnel restart to apply the independent unit. Merely upgrading Core does not
rewrite systemd units; do not replace a working custom unit without first
checking its `EnvironmentFile=` and credentials setup. The new installer
refuses to overwrite any pre-existing unit using `EnvironmentFile=`; preserve
its credential source and use a reviewed systemd drop-in to remove the old
Core dependency without touching the running tunnel (see `docs/SETUP.md`).

## Commands

| command     | purpose                                                      |
| ----------- | ------------------------------------------------------------ |
| `setup`     | configure local Core; optionally prepare an OpenAI profile |
| `init`      | create or update the low-level configuration                 |
| `serve`     | run the local HTTP MCP server                          |
| `connect`   | legacy combined foreground server and optional tunnel |
| `tunnel`    | inspect tunnel or manage its independent service |
| `status`    | show local server, tunnel provider, and tunnel service state (`--json`) |
| `doctor`    | check configuration, workspace, connection, and ports       |
| `logs`      | show collected systemd records or foreground guidance       |
| `service`   | manage the Linux Core systemd service only                          |
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

OpenAI Secure MCP Tunnel is the recommended optional ChatGPT connection path shown by
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
- **exec_command** — run a workspace command; return final output when it exits quickly, otherwise return a numeric `session_id`; `tty=true` opts into a real interactive terminal
- **write_stdin** — resume a live exec session, poll with empty input, or write stdin; Ctrl-C behaves like terminal input for TTY sessions and cancels pipe sessions
- **context** — cross-session rolling handoffs with `update`, immutable `checkpoint`, `resume`, and `list`

### Cross-session handoff

`context` keeps a small, persistent working state outside the project tree so a
new AI session can continue without replaying the old chat. A human-readable
context id such as `miodesk-maintenance` identifies the line of work. Its
`working_directory` is only a locator for where that work lives inside the
configured workspace; it never changes `workspace.root` or the server sandbox.

The active handoff is updated at meaningful state changes (goal, current state,
reasoning, decisions, blockers, next steps). `checkpoint` freezes the current
active handoff at an explicit or important milestone, while `resume` always
returns the newest active state. `list` discovers available context ids when a
new session does not already know which one to resume. Miodesk retains a small
internal revision history as a safety net, but normal resume output contains
only the current handoff and checkpoint count.

## ChatGPT and MCP clients

miodesk exposes ten native MCP tools. File and command tools use the host's
normal tool view, and `status` returns a compact read-only structured result.
No MCP Apps UI resource or output template is published. Exec sessions are
explicit handles threaded through `exec_command` and `write_stdin`; ordinary
commands use pipes, while `tty=true` allocates a Unix PTY for interactive use.
Local diagnostics remain available as JSON at `/api/status` and `/api/edits`.

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
