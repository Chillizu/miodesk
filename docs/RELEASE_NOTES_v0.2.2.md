# miodesk v0.2.2

miodesk is a lightweight local MCP bridge for ChatGPT and coding agents. This
release focuses on **a dependable first-run experience**, correct file tools,
and independently managed Core and OpenAI Tunnel processes.

## Highlights

- **Simpler first run:** verified shell installer, clearer PATH diagnostics,
  local-first README, noninteractive setup fix, and automated fresh-workspace
  MCP smoke tests. OpenAI credentials are **not required** for local use.
- **More reliable tools:** streaming reads beyond the first 512 KiB, consistent
  literal search using ripgrep or the builtin fallback, workspace-relative
  paths from `list`, and explicit truncation signals for command output.
- **Independent lifecycle:** `miodesk.service` (Core) and
  `miodesk-tunnel.service` (optional official OpenAI `tunnel-client`) are
  managed independently; Tunnel forwards MCP and never runs JavaScript.
- **Experimental Code Mode on Linux:** opt in using
  `[codemode] enabled = true` in `config.toml`. An additional read-only
  `code` tool composes `read/search/list` inside a time- and memory-limited
  JavaScript worker with bounded IPC. Tests cover errors, cancellation,
  resource limits, JSON result types and leakage of script exception text
  into persistent Core logs.

## Code Mode limitations

**Code Mode is experimental, Linux-only and disabled by default.** It is a
minimal, Codex-inspired JavaScript tool composition adapter, **not Codex's
V8 Code Mode host**. No shell, write, Node.js or network APIs are exposed to
scripts. The worker uses the same Unix account as Core: its resource limit
and host API allowlist are **not a full OS security sandbox**. Do not expose
it to scripts from unrelated untrusted principals. The original ten MCP
tools remain available regardless of Code Mode.

[Code Mode guide](https://github.com/Chillizu/miodesk/blob/v0.2.2/docs/CODEMODE.md) ·
[Quick start](https://github.com/Chillizu/miodesk/blob/v0.2.2/README.md) ·
[Security model](https://github.com/Chillizu/miodesk/blob/v0.2.2/docs/SECURITY.md)

## Install

On Linux or macOS:

```sh
curl -fsSL https://raw.githubusercontent.com/Chillizu/miodesk/main/scripts/install.sh | sh
export PATH="$HOME/.local/bin:$PATH"
miodesk version
```

The installer verifies SHA-256 checksums of the tagged Release binary. Manual
Windows amd64 downloads are provided below. No OpenAI Tunnel is installed or
started automatically. Check the README for the local MCP configuration and
optional ChatGPT connection walkthrough.

## Release artifacts

Five cross-compiled binaries, `install.sh`, `checksums.txt`, and
`manifest.json` are published below. Checksums can be verified before use.
