---
name: miodesk-workspace
description: Use the connected miodesk MCP service to inspect and edit local workspace files, run bounded commands, check status, or resume work across sessions.
---

Use miodesk when the user asks to inspect or work in the connected local workspace. Keep the workflow small, fast, and predictable.

- Inspect before changing: use `status` when connection state matters, and `list`, `search`, or `read` to locate relevant files.
- Change only what the user requested. File tools stay inside the configured workspace; use `edit` for targeted changes and `delete` only when clearly requested.
- Run bounded commands with `exec_command`. Its working directory is workspace-contained, but the process uses the current user's OS permissions and is not an OS filesystem sandbox. Never escalate privileges or silently install dependencies.
- Continue a returned `session_id` with `write_stdin` to poll, send input, or cancel the same process.
- Report errors, partial results, and completed actions accurately.
