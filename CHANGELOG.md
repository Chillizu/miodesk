# Changelog

## Unreleased

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
