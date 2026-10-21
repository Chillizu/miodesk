# ChatGPT connection and plugin integration (English)

中文：[CHATGPT.md](CHATGPT.md).

miodesk Core is an independent local MCP server whose default endpoint is
`http://127.0.0.1:8787/mcp`. Local use does **not** require ChatGPT or
OpenAI Secure MCP Tunnel. A remote ChatGPT session cannot reach
`127.0.0.1` on the user's computer directly.

## 1. Connect to ChatGPT through the official Tunnel

For supported ChatGPT plans/workspaces, use the official
[Secure MCP Tunnel](https://developers.openai.com/api/docs/guides/secure-mcp-tunnels).
This is an **optional, separate client process** that opens an outbound
HTTPS path to OpenAI and forwards MCP calls to the loopback Core; it does
not embed or replace the miodesk server.

See [SETUP.md](SETUP.md) for the full checklist:

1. Verify miodesk Core works locally with `setup`, `doctor`, and `status`.
2. Have the user create a Platform tunnel and associate it with the
   correct Platform organization and target ChatGPT workspace.
   Tunnel creators require Tunnels Read + Manage; tunnel users require
   Tunnels Read + Use. ChatGPT private custom MCP permissions are separate.
3. Install the latest official `tunnel-client` and keep the runtime
   API key in a private local file, never in chat or Git.
4. Configure the local official client profile:

   ```sh
   miodesk setup --tunnel-id tunnel_YOUR_ID --runtime-key-file /absolute/path/to/runtime-key
   miodesk doctor
   tunnel-client doctor --profile miodesk --explain
   ```

5. Keep Core and the official tunnel-client running and ready.
   `tunnel-client run --profile miodesk` is a foreground command.
   `miodesk connect` is the older combined foreground shortcut: use it
   **only if another Core and Tunnel instance are not already running**.
6. In **ChatGPT Plugins → Add custom MCP server**, select
   **Connection: Tunnel**, choose the correct tunnel, review
   authentication/access/risk prompts, and test a real read-only `status`
   MCP call from ChatGPT. UI labels and availability depend on account
   and workspace permissions; check the official
   [ChatGPT developer mode guide](https://help.openai.com/en/articles/12584461-developer-mode-and-full-mcp-connectors-in-chatgpt).

A green `/healthz` or tunnel ready message is **not** sufficient to prove
ChatGPT can use the tool; complete setup only after the model-side call works.

## 2. Lifecycle and tool schema refresh

Linux persistent deployments can use two separate systemd user services:

```sh
miodesk service install
miodesk service start
miodesk tunnel service install
miodesk tunnel service start
```

Do this **after** verifying the foreground connection and obtaining the
user's approval. Inspect existing unit files, custom `EnvironmentFile`
credentials and dependency overrides before modifying the tunnel service.
The Core and Tunnel service lifecycles are intentionally independent.

The standard MCP `tools/list` exposes **10 native tools**. An explicitly
enabled Experimental Linux Code Mode adds the eleventh, read-only
`code` tool (see [CODEMODE.md](CODEMODE.md)); this is not required for
ChatGPT access.

**Tunnel transports calls but cannot refresh ChatGPT's stored action schema.**
After tools change, use the ChatGPT action-management **Refresh** workflow.
New actions may be disabled initially. If a client still displays legacy
`command_start`/`command_poll` tools while Core advertises
`exec_command`/`write_stdin`, refresh the connection; do not reintroduce
retired tools into Core's published `tools/list`.

## 3. Optional private plugin package

`plugins/miodesk/` contains separate **private ChatGPT plugin** packaging
material. It is not Core or an OpenAI Tunnel installer.

The Agent Plugins manifest is `plugins/miodesk/plugin.json`. It refers
to an already registered MCP app using a local `.app.json` mapping.
The repository deliberately tracks only `.app.example.json`; a real
account-specific ID is never included in source.

The operator can copy `.app.example.json` to ignored `.app.json`,
enter the actual registered app ID, verify it in ChatGPT, and use
Plugin Creator/the current Plugins settings to package and test it.
`.codex-plugin/plugin.json` is retained as an older-client compatibility
manifest. The plugin reuses existing MCP and Tunnel connectivity.

Installing miodesk or publishing a GitHub Release **does not**
automatically republish a ChatGPT plugin or enable model-side actions.
Secure MCP Tunnel for private development does not by itself meet the
stable public HTTPS endpoint requirements for public plugin submission.

## 4. Diagnostics

```sh
miodesk status
miodesk doctor
miodesk tunnel doctor
tunnel-client doctor --profile miodesk --explain
```

Inspect `systemctl --user cat miodesk-tunnel.service` before any service
migration. Linux service logs are available with
`miodesk logs --follow --json`; no API key, token, file text or
Authorization header should appear in normal structured logs.

Other useful references:
[AI-guided setup](AGENT_SETUP.md),
[device setup](SETUP.md),
[security model](SECURITY.md),
[Chinese ChatGPT guide](CHATGPT.md).
