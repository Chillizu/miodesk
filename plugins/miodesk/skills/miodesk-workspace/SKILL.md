---
name: miodesk-workspace
description: Use the connected miodesk MCP service to inspect and change local workspace files, run commands, check connection health, and resume project work.
---

Use the smallest tool that can answer the task; avoid unnecessary terminal output and repeated status calls.

- **Locate → inspect → change → verify.** Use `list` for paths and directory layout, `search` for literal text inside files, and `read` with a focused `offset` (1-based) and `limit` for surrounding lines. All returned file paths are relative to the workspace root and can be reused directly. `search` is **not** regex; use `exec_command` with an appropriate search command when regular expressions are required.
- **Prefer file tools for file edits.** `edit` matches `old` exactly once by default; if content may have changed, read it again rather than guessing. For range edits provide the exact `expected` text. A failed edit batch changes no files. `write` replaces the entire file; do not use it for small changes. Only `delete` paths the user actually wants removed, and inspect before recursive deletion.
- **Use `exec_command` for build/test/git and genuinely shell-based work.** Set `workdir` explicitly for a project instead of assuming the workspace root is the repository; each call gets a fresh working directory. On Unix the shell is `/bin/sh` (not necessarily Bash). Commands run under the user's real OS permissions and are **not OS-sandboxed**, even though the starting workdir is validated. Never elevate privileges or silently install dependencies.
- **Continue only real sessions.** An `exit_code` means a command is finished; a `session_id` means call `write_stdin` to poll (empty `chars`), send input, or cancel. For interactive terminal programs request `tty=true`; otherwise use pipes. Session output is **incremental**, not a full transcript on each poll. When `truncated=true`, captured output is incomplete—narrow the command or redirect to a file and inspect a focused portion, rather than asserting unseen output.
- **Respect incomplete results.** `read`, `search`, and `list` can return `truncated=true`; this is not proof the requested content does not exist outside the returned window. Prefer another focused request before concluding absence. `status` is for connection diagnosis, not a prerequisite to every file call.
- **Verify and report.** Run relevant bounded checks after changes, distinguish tested outcomes from assumptions, and report errors and any edits or commands that were not executed.
