# Changelog

## Unreleased

- Added persistent cross-session working contexts: one `context` tool can keep a
  rolling active handoff, freeze immutable checkpoints, resume the latest state,
  and list available context ids. `working_directory` is a workspace-contained
  locator only and does not change the configured workspace root.
- Declared an explicit deny-by-default MCP Apps CSP for the diagnostics widget:
  empty network/resource allowlists, no nested-frame allowance, and one shared
  metadata helper for both resource registration and returned resource contents.
- Collapsed the old model-facing command lifecycle into a Codex-shaped
  `exec_command` + `write_stdin` surface. Quick commands finish inline; longer
  commands return a numeric `session_id`, and later calls consume only new output,
  can write stdin, or cancel pipe-backed sessions with Ctrl-C. The existing bounded
  session manager remains internal, so the safety limits survive without exposing
  lifecycle bookkeeping. `tty=true` now allocates a real Unix PTY for interactive
  programs; pipe mode remains the default, and terminal multiplexers such as tmux
  are deliberately not part of the MCP contract.
- Added a narrow migration shim for stale clients that still call the formerly
  advertised `command`, `command_start`, `command_poll`, or `command_cancel`
  tools. The shim rewrites calls into the unified exec surface without
  re-advertising retired tools and is intended to be removed after client-side
  schema caches age out.
- Kept command execution UI-free. The embedded UI is diagnostics-only; command
  sessions use lightweight native invocation feedback and never advertise an
  output template or create polling cards.
- Tightened process lifecycle cleanup: failed/pre-cap exec starts close their
  stdin resources, server shutdown gives all active exec sessions one shared
  bounded grace period, while process-backed tunnel providers share bounded
  stop logic and acquisition timers are explicitly stopped on early return.
- Added a companion `miodesk-tunnel.service` for configured OpenAI Secure MCP
  Tunnel profiles, so Linux service commands supervise the loopback MCP server
  and outbound tunnel together.
- Made `doctor`, `status`, and `connect` report the tunnel lifecycle
  explicitly and avoid starting a duplicate tunnel-client when the persistent
  tunnel service is already running.
- Included the tunnel companion journal in `miodesk logs`.
- Hardened persistent OpenAI tunnel reuse against port/profile mismatches and
  completed tunnel reporting in status JSON recovery and the diagnostics widget.

## 0.2.1 — 2026-09-08

- Kept valid server state after a transient health-check failure so `status`
  can recover without requiring a service restart.
- Made `doctor` verify the exact occupied port is served by miodesk instead
  of attributing it to any active systemd user service.
- Kept the embedded MCP Apps identity aligned with the binary build version.

## 0.2.0 — 2026-09-07

- Added a new-device `miodesk setup` flow with a stable local port, canonical
  workspace path, secure runtime-key checks, and OpenAI Secure MCP Tunnel
  profile generation through the official `tunnel-client`.
- Made OpenAI Secure MCP Tunnel the default for fresh configurations while
  keeping legacy connection values readable for existing installations.
- Kept the primary CLI native-first and moved regular tool results away from
  the optional widget; status and long-running command lifecycle results keep
  the rich UI.
- Added structured diagnostics, request correlation, secret-safe logs, and
  release preflight documentation.
- Changed the Go module path to `github.com/Chillizu/miodesk` so source
  installation works from the public repository.
- Release packaging now emits platform binaries, checksums, and an updater
  manifest.
- Licensed the repository under MPL-2.0; see `LICENSE`.
