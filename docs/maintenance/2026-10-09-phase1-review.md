# Phase 1 maintenance review — 2026-10-09

## Scope and safety

This maintenance pass started from `4c02322` on `mio/maintenance-phase-a`. It reviews, verifies and groups pre-existing working-tree changes; it does not upgrade the installed binary, restart the running service, publish a GitHub branch or change the active ChatGPT connection.

The pre-review working tree (tracked patch, index patch, untracked plugin files, original HEAD and status) was preserved under `$HOME/.local/state/miodesk-maintenance/phase1-20261009/` and checked against SHA256SUMS before changes.

## Review decisions

1. **Retire the embedded MCP Apps diagnostics UI:** remove `web/widget` assets and the obsolete `/`, `/widget` and `/preview` handlers; keep ten standard MCP tools, structured `status`, command/session behavior and local JSON diagnostics. `config.toml` files with a historical `[widget]` table can still be loaded; subsequent saves omit that retired table. This removes a UI integration surface and therefore requires a host compatibility check before any release or deployment.
2. **Keep native tool metadata minimal:** invocation labels remain on `status`, `exec_command` and `write_stdin`; no tool advertises `_meta.ui` or an output template, and `resources/list` is empty. Legacy command-name compatibility has not been expanded or removed.
3. **Preserve documentation history:** current docs reflect native-only tools; older Phase A connector records are explicitly marked as historical rather than overwritten. The refreshed ChatGPT action schema was reported by the user previously, but the current host action-level acceptance test remains a separate gate.
4. **Add a private-safe ChatGPT plugin packaging draft:** the plugin manifests, skill and copied logo are maintained in `plugins/miodesk/`. The account-specific `.app.json` is intentionally **not tracked**. `.app.example.json` is checked in; operators must copy it, set the actual app ID and verify the binding before packaging. The existing local `.app.json` has not been rewritten or published.
5. **Do not combine future refactors with this pass:** Core/ChatGPT logical separation, permission profiles, partial-success directory-listing behavior, OS portability and update mechanism changes require independent proposals and tests.

## Local commits

- `ce421af` — `refactor: retire MCP Apps widget and retain native status`
- `d564a59` — `docs: align maintenance guidance with native MCP tools`
- `7a02d1d` — `build: add private-safe ChatGPT plugin packaging draft`

## Verification

- `git diff --check` and standard Go formatting checks passed.
- `go vet -all ./...` and `go test -vet=all ./...` passed.
- `scripts/release-check.sh` passed, including dependency verification, race-enabled Go tests, build and isolated installer smoke test.
- A separately built candidate used temporary XDG data/config/cache directories, an isolated writable workspace and a random loopback port. The native MCP smoke client confirmed connection, exactly ten registered tools, structured `status`, short command, long command polling and stdin write/cancel; `/healthz` returned success and the retired `/widget` endpoint returned 404. The candidate was then stopped. No production service was touched.
- Plugin JSON manifests, documented asset files and example binding were checked. No private `.app.json` is included in tracked paths.

## Remaining gates

- **Before deployment/release:** compare the host's actual ChatGPT actions and existing UI cache against the candidate. Removing the previous `ui://miodesk/status...` resource is a deliberate compatibility change. Coordinate explicit connector refresh and a rollback binary before any production switch.
- **Before plugin upload:** verify the registered ChatGPT MCP app ID, ensure the local `.app.json` resolves to that app, and validate the archive through the plugin system. Do not infer that a local JSON parse proves installation compatibility.
- **Phase 2:** define the boundary between generic `miodesk` MCP Core and optional ChatGPT adapter/tunnel packaging without changing the ten tool schemas by accident.
- **Future reliability work:** make directory walking report partial results when inaccessible entries such as `/home/.snapshots` are encountered, but do not bypass normal Unix permissions.

No GitHub push, service restart, tunnel alteration, system-level permission change or plugin publication occurred in this phase.
