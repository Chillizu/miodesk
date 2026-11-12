# External MCP plugins

miodesk Core can host opt-in, independent MCP plugins over stdio without embedding the plugins' application-specific protocols, dependencies, or credentials. This works with any external program that implements a compatible **stdio MCP server**.

## Trust boundary and default behavior

- External plugins are **disabled by default**: the original 10 Core tools stay unchanged.
- Enabling is an explicit operator configuration, not discovery of arbitrary executables from `PATH`.
- Every plugin needs a private JSON manifest; the manifest must name an **absolute executable path** and an explicit per-tool grant.
- The manifest directory must be a real directory and not writable by group/others; each manifest must be a real regular file and not writable by group/others. Keep both owner-only (0700 directory, 0600 files) as best practice.
- No shell is used. Only `HOME`, `USER`, `PATH`, `LANG` and XDG directory environment values are inherited. Sensitive Core keys and tunnel credentials are not injected.
- Each declared plugin tool appears as `<id>__<tool_name>` in the Core's tool catalog; undeclared tools are not exported. `plugins_status` lists active plugin IDs and exported tools without command lines or secrets.
- Write authorization comes from the trusted manifest and the operator's `plugins.allow_writes` gate, never from third-party annotations. A tool granted `read` must **also** advertise `readOnlyHint: true`; this hint can veto exposure but can never authorize a write. Even read-only tools are code running in the Core OS user: **this is not an OS sandbox**. Only install code you trust.
- MCP tool calls have a 90 s timeout and bounded payload/result sizes; startup/discovery is bounded and errors are isolated so Core can still start.
- Plugin process lifecycle is tied to the Core instance; stdio is shut down when the Core exits. Plugin registration is currently startup-only: changes require a controlled Core restart, not a live reload.
- The existing OpenAI Secure MCP Tunnel continues to forward the same Core server; no separate public port or external tunnel is required for plugins.

## Config

Add to the normal miodesk `config.toml` (no change by default):

```toml
[plugins]
enabled = true
directory = "/home/you/.config/miodesk/plugins.d"
allow_writes = false
```

Install a plugin declaration there, for example `dsh.json`:

```json
{
  "id": "dsh",
  "command": "/absolute/path/to/miodesk-dsh",
  "args": [
    "--url", "http://127.0.0.1:3080",
    "--token-file", "/home/you/.config/miodesk-dsh/launch-token",
    "--state-dir", "/home/you/.local/state/miodesk-dsh"
  ],
  "tools": [
    { "name": "dsh_status", "permission": "read" },
    { "name": "dsh_sessions", "permission": "read" },
    { "name": "dsh_read", "permission": "read" },
    { "name": "dsh_turn_status", "permission": "read" }
  ]
}
```

Granting writes requires both `plugins.allow_writes = true` and an explicit `{ "name": "...", "permission": "write" }` grant. The plugin itself may impose extra gates such as `--allow-send`; the DSH plugin does. A writable tool declaration in a read-only Core config is rejected instead of implicitly authorized.

Use `miodesk doctor` and `plugins_status` after a safe restart. The MCP action inventory on ChatGPT may require an explicit refresh/approval for new names.

## Security limits and future work

This provides process modularity and explicit MCP exposure, **not per-plugin filesystem sandboxing**. The child process inherits the OS UID and can open files that UID can open. For untrusted plugins, run a separate OS account/container with least privileges and connect using an independently secured transport rather than launching it from the Core.

Future extension candidates: per-plugin OS sandbox policy and subprocess privilege reduction, safe hot-reload with replacement generations, bounded restart/backoff for crashed processes, plugin signature checks, separate local-UI management, and external authenticated Streamable HTTP client support. None is silently implied by this MVP.
