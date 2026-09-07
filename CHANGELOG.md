# Changelog

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
