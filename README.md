<p align="center">
  <img src="plugins/miodesk/assets/miodesk-logo.png" width="150" alt="miodesk logo">
</p>

<h1 align="center">miodesk</h1>
<p align="center"><strong>Give your AI assistant a small, local workspace bridge.</strong></p>
<p align="center"><strong>English</strong> · <a href="README.zh-CN.md">简体中文</a></p>

<p align="center">
  <a href="https://github.com/Chillizu/miodesk/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/Chillizu/miodesk/actions/workflows/ci.yml/badge.svg"></a>
  <a href="LICENSE"><img alt="License: MPL-2.0" src="https://img.shields.io/badge/license-MPL--2.0-blue"></a>
</p>

**miodesk** is a lightweight MCP server written in Go. It connects AI assistants to the files and developer tools on **your own machine**, with a small, predictable interface. Use it with local MCP clients, coding agents, or ChatGPT through an **optional, separate** OpenAI Secure MCP Tunnel.

- **10 focused tools:** read, search, list, write, edit, delete, command execution, interactive session continuation, cross-session handoffs, and status.
- **One binary:** no Node.js, Python, or Rust runtime required for the released executable.
- **Local by default:** starts on `127.0.0.1`; file tools enforce a configured workspace boundary.
- **Predictable commands:** bounded output, resumable `exec_command` / `write_stdin`, and guarded file edits.
- **Experimental Code Mode on Linux (opt-in):** combine read-only tool calls in a resource-limited JavaScript worker (disabled by default).

> **Important security distinction:** file tools enforce the workspace boundary, but **shell commands run with your normal OS user permissions** and are not filesystem-sandboxed. Only connect clients you trust. [Security details](docs/SECURITY.md).

## One-prompt setup with ChatGPT (no connector needed yet)

In an ordinary ChatGPT chat, paste the following. ChatGPT can **read the public setup guide over the web and walk you through installation**; it cannot operate your local computer until miodesk is actually connected.

```text
Read https://raw.githubusercontent.com/Chillizu/miodesk/main/docs/AGENT_SETUP.md and guide me step by step to install miodesk and use it in ChatGPT. Give me one copyable command block at a time.
```

**[Agent setup guide](docs/AGENT_SETUP.md) · [Feedback](docs/FEEDBACK.md) · [中文版引导提示词](README.zh-CN.md#用-chatgpt-一句话引导安装)**

## Quick start · local MCP

**1. Install** (Linux or macOS):

```sh
curl -fsSL https://raw.githubusercontent.com/Chillizu/miodesk/main/scripts/install.sh | sh
export PATH="$HOME/.local/bin:$PATH"  # for this terminal, if needed
miodesk version
```

The installer downloads a platform-specific **GitHub Release**, checks its SHA-256 checksum, and installs it to `~/.local/bin`. It does **not** create services, start processes, modify your project, or configure OpenAI credentials.

Prefer to inspect the script first? [Read install.sh](scripts/install.sh), or download it before running. Windows x64 users can install from PowerShell with the verified [install.ps1](scripts/install.ps1):

```powershell
$installer = Join-Path $env:TEMP "miodesk-install.ps1"
Invoke-WebRequest "https://raw.githubusercontent.com/Chillizu/miodesk/main/scripts/install.ps1" -OutFile $installer
# Review the downloaded script before running it.
& $installer
& "$env:LOCALAPPDATA\\Programs\\miodesk\\miodesk.exe" version
```

The Windows installer does not modify PATH, policy, startup programs or services. The shell installer targets Linux/macOS; Windows ARM64 has no native published asset.

**2. Choose a workspace and configure it:**

```sh
mkdir -p "$HOME/miodesk-demo"
cd "$HOME/miodesk-demo"
miodesk setup --workspace "$PWD"
miodesk doctor
```

Substitute your own project directory when ready. `setup` stores its canonical path as the allowed workspace root and defaults to **local-only** MCP on port `8787`. Nothing connects to ChatGPT automatically. Without flags, `miodesk setup` opens an interactive wizard **only in a real terminal**. Since **v0.2.3**, `setup --non-interactive --workspace ...` explicitly disables prompts and `preflight --json` provides read-only system checks. Earlier versions still support `setup --workspace ...`.

**3. Start Core:**

```sh
miodesk serve
```

Core now serves MCP at `http://127.0.0.1:8787/mcp` on your machine. Leave that terminal running; press **Ctrl-C** to stop. Run `miodesk status` from another terminal to inspect the connection.

### Connect a local MCP client

Many clients can launch miodesk over **stdio**, without running the HTTP service. Point the client's MCP server configuration at the installed binary:

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

Replace the binary path with the result of `command -v miodesk`. The client starts and manages this process; you do **not** need a second `miodesk serve` instance for stdio. The client can then invoke the normal file, command and status tools.

## Connect ChatGPT · optional OpenAI Tunnel

ChatGPT cannot reach your computer's `127.0.0.1` address directly. For supported ChatGPT/Platform setups, the official [OpenAI Secure MCP Tunnel](https://developers.openai.com/api/docs/guides/secure-mcp-tunnels) provides an **outbound connection** without exposing a public TCP port.

This is a **separate component** from miodesk Core. It requires an OpenAI Platform tunnel, the official `tunnel-client`, and a runtime key saved to a local, user-readable-only file. Follow [the complete setup guide](docs/SETUP.md) for those prerequisites and ChatGPT configuration.

After obtaining the tunnel ID and key file:

```sh
miodesk setup \
  --tunnel-id tunnel_YOUR_ID \
  --runtime-key-file /absolute/path/to/runtime-key
miodesk doctor
miodesk connect
```

Stop any existing foreground `miodesk serve` before `miodesk connect`: this compatibility command runs **both Core and Tunnel** until you press Ctrl-C. On Linux, you can instead manage independent persistent services:

```sh
miodesk service install
miodesk service start
miodesk tunnel service install
miodesk tunnel service start
```

**For first setup, use foreground Core and Tunnel processes until ChatGPT actually calls `status`.** Permanent systemd services are a separate, optional **post-connection** step; do not install them by default. Core restarts do **not** require Tunnel restarts, and Tunnel is not needed for local clients. Existing custom units may need a [credential-preserving migration](docs/SETUP.md#4-start-the-connection).

## What can it do?

| Tool | Typical use |
| --- | --- |
| `read` | Read focused line ranges from a text file |
| `search` | Find **literal** text in the workspace; get filenames and line numbers |
| `list` | Browse files and directories with bounded depth |
| `write` / `edit` / `delete` | Make scoped, validated filesystem changes |
| `exec_command` | Run a shell command; get output or a resumable session ID |
| `write_stdin` | Poll a session, send input, or interrupt it |
| `context` | Save and resume a small cross-session working handoff |
| `status` | Check the connection and runtime |

For regular coding, a natural workflow is **search → read → edit → test**, using `exec_command` with an explicit project working directory. File tool paths are relative to the configured workspace root.

### Code Mode (Experimental · Linux only)

Code Mode optionally adds an eleventh MCP tool, `code`, which lets an assistant compose `read/search/list` calls inside a bounded JavaScript worker. It is **off by default**, **read-only**, and does not add shell or write permissions. This is process/resource containment, **not** a full OS sandbox. The experimental API and limits may change in later releases; it does not implement Codex's V8 `exec`/`wait` host.

```toml
[codemode]
enabled = true
```

Add that section to your [configuration](#configuration) and restart **Core only**. Details: [Code Mode (Experimental) guide and safety](docs/CODEMODE.md).

## Configuration

Configuration is stored in `~/.config/miodesk/config.toml` (or `$XDG_CONFIG_HOME/miodesk/config.toml`):

```toml
[server]
host = "127.0.0.1"
port = 8787

[workspace]
root = "/absolute/path/to/your/project"

[tunnel]
provider = "local"  # optional OpenAI tunnel is not configured
```

Run `miodesk config` to locate the file, `miodesk workspace` to see the root, or `miodesk doctor` to diagnose problems. The [advanced configuration and device guide](docs/SETUP.md) covers custom ports, separate services, credential files, migrations and alternative HTTPS endpoints.

## Install from source / development branch

The default installer downloads the latest **published** [GitHub Release](https://github.com/Chillizu/miodesk/releases), not necessarily the newest `main` branch. Experimental Code Mode first ships in **v0.2.2**; check the installed version with `miodesk version` and consult the [changelog](CHANGELOG.md).

To test the **current repository code** instead, install Go **1.26.6 or newer**:

```sh
git clone https://github.com/Chillizu/miodesk.git
cd miodesk
go build -buildvcs=false -o miodesk ./cmd/miodesk
./miodesk version
```

The source build is not the same as a verified, versioned Release asset; review and test it before using it with sensitive workspaces. `go install github.com/Chillizu/miodesk/cmd/miodesk@latest` follows the **latest module version tag**, not necessarily `main`.

## Troubleshooting

| Symptom | What to check |
| --- | --- |
| `miodesk: command not found` | Add `~/.local/bin` to `PATH`, or use the full path shown by the installer |
| Port `8787` already in use | Run `miodesk status` before starting a second server; change ports deliberately |
| Cannot access a file | Check `miodesk workspace` and permissions; file paths must stay inside the root |
| ChatGPT does not show new tools | Refresh the registered MCP app's tool list; Tunnel does not refresh ChatGPT schemas |
| Tunnel does not connect | Run `miodesk tunnel doctor` and follow [the setup guide](docs/SETUP.md) |
| Need logs | Run `miodesk logs --follow` (systemd) or inspect foreground terminal output |

## Feedback & bug reports

Found a setup blocker or connection problem? **[Report a bug](https://github.com/Chillizu/miodesk/issues/new?template=bug_report.yml)** or **[suggest an improvement](https://github.com/Chillizu/miodesk/issues/new?template=feature_request.yml)**. Include your platform, version and the first failed step, but **redact secrets, Tunnel IDs, paths and private logs**. For vulnerabilities use [private security reporting](https://github.com/Chillizu/miodesk/security/advisories/new). [Feedback guide / 中文反馈指引](docs/FEEDBACK.md).

## Development

```sh
go test -vet=all ./...
scripts/release-check.sh
bash scripts/install-test.sh
bash scripts/onboarding-test.sh
```

See [Agent-guided setup](docs/AGENT_SETUP.md), [Security](docs/SECURITY.md), [device setup](docs/SETUP.md), [ChatGPT-specific notes](docs/CHATGPT.md), [release checklist](docs/RELEASE.md), and the [changelog](CHANGELOG.md). Chinese: [README.zh-CN.md](README.zh-CN.md). Licensed under [MPL-2.0](LICENSE).
