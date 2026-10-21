# Context: persistent working handoffs

**English** · [简体中文](CONTEXT.zh-CN.md) · [README](../README.md)

The `context` MCP tool saves a **small, structured handoff for a long-lived line of work**. A later AI conversation or another MCP client can use the same context ID to recover the current goal, progress, decisions, unresolved questions, and next steps without replaying the conversation.

It is **not** a chat transcript, a model's hidden reasoning, an automatic memory mechanism, a task scheduler, or a version-control system. The client must explicitly call `context`; miodesk's sessionless HTTP transport does not know which chat owns a context.

## Mental model

- A stable `id` identifies a line of work across conversations and compatible clients.
- The **active handoff** is mutable. `update` patches it; `resume` reads it.
- **Revisions** retain the previous active handoff after an effective update.
- **Checkpoints** snapshot a milestone independently from revisions. They are immutable records; creating one does not roll back the active handoff.
- `list` discovers saved contexts, newest updated first. Neither `list` nor `resume` returns full internal history.

Use one ID per ongoing project or workstream (for example, `miodesk-maintenance`), not one per chat message.

## Actions

| `action` | Required fields | Behavior |
| --- | --- | --- |
| `update` | `id`, and content for a new context | Create a context or partially update an existing active handoff. Only effective changes increment `revision`. |
| `checkpoint` | `id`; existing handoff or initial content | Optionally apply an update, then append an immutable milestone snapshot. `label` is optional. |
| `resume` | `id` | Read the current active handoff with its revision and checkpoint count, **not** the complete historical snapshots. |
| `list` | `action` only | Return compact summaries of saved contexts sorted by `updated_at` descending (ID ascending on ties). |

The request field `action` is case-insensitive after trimming surrounding whitespace. A context `id` should match `^[a-z0-9][a-z0-9._-]{0,63}$` (1–64 characters); use lowercase ASCII letters, digits, dots, underscores, or hyphens, starting with a letter or digit. Supply the ID without leading or trailing whitespace.

## Request fields

| Field | Type | Meaning |
| --- | --- | --- |
| `action` | string | One of `update`, `checkpoint`, `resume`, `list`. |
| `id` | string | Stable context identifier; needed except for `list`. |
| `working_directory` | string | Existing directory inside the configured workspace; a locator for the work, **not** a change to `workspace.root` or the shell's current directory. |
| `summary` | string | Brief handoff overview. |
| `goal` | string | Goal of this workstream. |
| `current_state` | string | What is complete and what is in progress. |
| `approach_summary` | string | Short account of the current approach and its rationale; **not** private chain-of-thought. |
| `decisions` | string[] | Decisions worth preserving for a later session. |
| `open_questions` | string[] | Unresolved questions or blockers. |
| `next_steps` | string[] | Ordered next actions. |
| `clear_fields` | string[] | Explicitly clear selected scalar fields in `update` / `checkpoint`. |
| `label` | string | Optional name for a `checkpoint` milestone. |

`working_directory` is resolved and verified against the workspace boundary and must be an existing directory. The stored display path is absolute; when a safe symlink or bind-mount path was supplied, the caller-facing spelling is preserved.

### Partial-update rules

- For strings (`summary`, `goal`, `current_state`, `approach_summary`), nonempty trimmed values replace existing values. An omitted or empty string **does not** clear a value.
- For list fields (`decisions`, `open_questions`, `next_steps`), an omitted field preserves its previous value; an explicit empty array `[]` clears it. Supplied arrays are trimmed, deduplicated, and have empty entries removed while preserving order.
- To clear a scalar, use `clear_fields` with any of `working_directory`, `summary`, `goal`, `current_state`, `approach_summary`. Clearing wins if the same field is also set in the request. This option is invalid for `list` and `resume`.
- If an existing `update` makes no change, it leaves its revision and timestamp unchanged. A new context requires actual handoff content.
- `checkpoint` can apply changes first. Even without a change, it appends a new checkpoint and advances `updated_at`, but does not increment `revision`.
- The older `active_reasoning` input is accepted only as a compatibility alias for `approach_summary`. Prefer the current field; if both are supplied, `approach_summary` wins. Unknown input properties are rejected.

## Typical MCP requests

The following are **tool argument objects**, not CLI commands. Call the native `context` MCP tool with each JSON object.

**1. Save a handoff:**

```json
{
  "action": "update",
  "id": "example-maintenance",
  "working_directory": "my-project",
  "summary": "Core upgrade verified; documentation remains.",
  "goal": "Finish the maintenance release safely.",
  "current_state": "Tests passed; release notes pending.",
  "approach_summary": "Keep Core and optional tunnel independently managed.",
  "decisions": ["Do not restart the tunnel for a Core-only update."],
  "open_questions": ["Does the client need a tool-schema refresh?"],
  "next_steps": ["Write release notes", "Review diff", "Publish"]
}
```

Replace `my-project` with an existing directory **inside your configured workspace**. If you do not need a directory locator, omit `working_directory`.

**2. Capture a milestone:**

```json
{
  "action": "checkpoint",
  "id": "example-maintenance",
  "label": "validation-passed",
  "current_state": "Release notes written and tests passed."
}
```

**3. Discover available contexts:**

```json
{"action": "list"}
```

**4. Continue the work in a different conversation:**

```json
{"action": "resume", "id": "example-maintenance"}
```

After resuming, verify the actual files and runtime state before executing the saved `next_steps`. A handoff is a description of past work, not a live probe.

## Return shapes

Responses have `kind: "context"` and an `action` matching the request:

- `update` / `resume`: `context` (active handoff)
- `checkpoint`: `context` plus the newly created `checkpoint`
- `list`: `contexts` (an array of compact summaries; an empty store returns `[]`)

The returned active `context` includes `id`, optional `working_directory`, `state` (the seven semantic fields above), `revision`, `checkpoint_count`, `created_at`, and `updated_at`. A `list` summary includes `id`, optional `working_directory` / `goal` / `current_state`, `revision`, `checkpoint_count`, and `updated_at`. A returned checkpoint includes `id`, optional `label` / `working_directory`, `created_at`, `revision`, and `state`.

## Storage, retention, and limitations

- One JSON file per context: `$XDG_DATA_HOME/miodesk/contexts/<id>.json`, defaulting to `~/.local/share/miodesk/contexts/<id>.json`. Records live **outside project repositories** and `config.toml`.
- The directory is restricted to mode `0700` and new files to `0600` on Unix. Writes use a temporary file, sync, and rename to replace the target.
- The active handoff is separate from up to **20 previous revisions** and up to **50 checkpoints** per context. Old retained records are evicted at those limits; checkpoint sequence numbers continue increasing.
- The MCP interface does **not** currently provide `history`, `get_checkpoint`, `rollback`, or `delete` actions. The retained records exist on disk but are not exposed by `resume` / `list`.
- Persistence survives chat changes and normal Core restarts on the **same machine and data directory**. It is not automatically synchronized between devices and is not pushed to GitHub.
- The store serializes operations within a Core process; it is not a distributed database or a cross-process coordination/locking service.
- Do not store credentials, API keys, full private logs, or unnecessary personal information. Treat any AI-produced handoff as untrusted until validated against current project state.

## When to update

Record meaningful state changes: completed tests, decisions, blockers, and a clear next action. Make checkpoints at milestones, not after every small command. Use `list` when the context ID is unknown, then `resume` the relevant workstream.

For live connection diagnostics, use the separate `status` tool. For trust boundaries, see [Security](SECURITY.md). Implementation: [`internal/contextstore/store.go`](../internal/contextstore/store.go).
