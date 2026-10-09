# Remove the MCP Apps Diagnostics UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove miodesk's MCP Apps diagnostics interface and its local widget pages while keeping the normal `status` tool, its structured result, command tools, and existing JSON diagnostics APIs usable.

**Architecture:** Keep `internal/adapter` only for lightweight tool invocation labels and status registration. Register `status` without UI metadata or resources; remove the embedded widget assets and local HTML routes. Preserve `/api/status` and `/api/edits`, and accept legacy `[widget]` config tables as ignored input so existing config files continue to load.

**Tech Stack:** Go, official MCP Go SDK v1.7.0, go-toml/v2, existing `net/http` server tests.

**Spec:** `docs/superpowers/specs/2026-09-23-miodesk-maintenance-design.md` §3.3. This plan supersedes that section's widget-failure fallback: the approved direction removes the widget entirely while keeping status available.

## Global Constraints

- Always lowercase `miodesk`; keep Go and the official MCP SDK for protocol handling.
- Keep the design Native-first; `status` remains read-only and `exec_command` / `write_stdin` remain unchanged.
- Preserve workspace/security boundaries and existing access middleware on JSON endpoints.
- Do not add runtime dependencies or introduce a replacement UI.
- Preserve all pre-existing unrelated working-tree changes.

## Review Focus

- A client with cached widget metadata must still be able to call `status`; verify the new server advertises no UI metadata and returns the existing structured payload.
- A legacy config containing `[widget] theme = "dark"` must load without error, must not affect behavior, and must not be written back by a later config save.
- The `/api/status` JSON shape, including the legacy `theme` property, must remain available; return `"auto"` for the now-inactive theme field.
- `/`, `/widget`, and `/preview` must no longer serve HTML; `/healthz`, `/api/status`, and `/api/edits` must remain available behind existing middleware.
- `resources/list` must be empty and the ten existing tools, including `status`, `exec_command`, and `write_stdin`, must remain registered.

---

### Task 1: Remove MCP Apps metadata and resource registration

**Files:**
- Modify: `internal/adapter/adapter.go`
- Modify: `internal/adapter/adapter_test.go`
- Modify: `internal/server/server.go`
- Modify: `internal/server/server_test.go`

**Interfaces:**
- Keep `adapter.ToolMeta(name)` and invocation labels for `exec_command`, `write_stdin`, and `status`.
- Replace widget-oriented `adapter.Attach` inputs with status-only registration: `RegisterStatus(s *mcp.Server, data Data)`.
- Keep the `status` input and output schemas, annotations, and structured payload unchanged.

- [ ] **Step 1: Add the failing status-without-Apps MCP test**

In `internal/adapter/adapter_test.go`, adapt the existing real SDK client harness to register status with the current `Attach(server, widget.Static, data)` signature. Assert there is exactly one tool in the isolated adapter server; its name is `status`; `_meta.ui` and `openai/outputTemplate` are absent; invocation labels remain; `resources/list` is empty; and a status call returns the handler's structured payload. The core assertions are:

```go
if _, ok := listed.Tools[0].Meta["ui"]; ok {
	t.Fatalf("status metadata must not advertise UI: %v", listed.Tools[0].Meta)
}
if _, ok := listed.Tools[0].Meta["openai/outputTemplate"]; ok {
	t.Fatalf("status metadata must not advertise a template: %v", listed.Tools[0].Meta)
}
resources, err := session.ListResources(ctx, &mcp.ListResourcesParams{})
if err != nil || len(resources.Resources) != 0 {
	t.Fatalf("resources/list = %+v, err = %v; want no resources", resources, err)
}
```

Run: `go test ./internal/adapter -run '^TestRegisterStatusWithoutAppsUI$' -count=1`
Expected: the test fails because current `Attach` advertises widget metadata and registers resources when widget assets assemble.

- [ ] **Step 2: Add the failing full-server metadata assertion**

Update the existing MCP integration test in `internal/server/server_test.go` to assert that `resources/list` is empty, no listed tool has `_meta.ui` or `openai/outputTemplate`, and the existing ten tool names and exec invocation labels remain. Keep the current assertions for command schemas, annotations, and structured status output.

Run: `go test ./internal/server -run '^TestMCPAppsDashboard$' -count=1`
Expected: FAIL on the current widget resource and status metadata assertions; the updated assertions should specifically identify those remnants.

- [ ] **Step 3: Register status without UI support**

Remove widget URIs, MIME type, HTML assembly, resource metadata, and widget registration from `internal/adapter/adapter.go`. Add status-only registration that keeps the existing output and error behavior:

```go
func RegisterStatus(s *mcp.Server, data Data) {
	s.AddTool(dashboardTool(), func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		out, err := data(ctx)
		if err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{StructuredContent: out}, nil
	})
}
```

Make `dashboardTool()` set `invocationMeta("status")` directly. Update `server.New` to call `RegisterStatus(s.mcp, dataHandler)`. Keep `ToolMeta` behavior for command and status invocation labels.

- [ ] **Step 4: Run the focused MCP tests**

Run: `go test ./internal/adapter ./internal/server -run 'TestRegisterStatusWithoutAppsUI|TestMCPAppsDashboard|TestServerToolListMatchesMetadata' -count=1`
Expected: PASS, with no widget resources or UI metadata and unchanged native tool contracts.

### Task 2: Remove local widget serving and retire its config

**Files:**
- Modify: `internal/server/server.go`
- Modify: `internal/server/server_test.go`
- Modify: `internal/server/security_test.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `internal/cli/cli.go`
- Modify: `internal/cli/commands.go`
- Modify: relevant `internal/cli` tests
- Delete: `web/widget/embed.go`
- Delete: `web/widget/static/`

**Interfaces:**
- Keep `/healthz`, `/api/status`, and `/api/edits` and their current access middleware.
- Keep `LocalStatus` JSON fields; set `theme` to `auto` as a compatibility value.
- Do not expose HTML at `/`, `/widget`, or `/preview`.
- Drop `Config.Widget` from active config and rely on the TOML decoder's existing unknown-field tolerance for old `[widget]` tables.

- [ ] **Step 1: Add failing HTTP and config compatibility tests**

Add a server test using `httptest.NewServer(newTestServer(t).Handler())`. Request `/`, `/widget`, and `/preview` and expect 404; request `/healthz`, `/api/status`, and `/api/edits` and expect 200. Decode `/api/status` and assert `kind == "status"` and `theme == "auto"`. Add a config test that loads a file containing `[widget] theme = "dark"`, saves the loaded config, and asserts loading succeeds and the saved file contains no `[widget]` table. Its compatibility input is:

```toml
[server]
port = 8787

[widget]
theme = "dark"
```

Run: `go test ./internal/server ./internal/config -run 'TestHandlerRemovesWidgetRoutesAndKeepsJSONAPIs|TestLegacyWidgetConfigIgnored' -count=1`
Expected: FAIL because the widget routes currently serve HTML and config still serializes/uses the widget theme.

- [ ] **Step 2: Remove HTML routes and embedded assets**

Remove the `/preview`, `/widget`, and static-root handlers and their widget imports from `internal/server/server.go`. Delete `web/widget/embed.go` and its static assets. Keep JSON API handlers and edit-history behavior unchanged. With no root handler, Go's `ServeMux` returns 404 for `/`. Remove widget-specific comments and any imports made unused by this change.

- [ ] **Step 3: Retire widget theme behavior without breaking old configs**

Remove the `Widget` config type, `Config.Widget`, its defaulting, validation, and related tests. `toml.Unmarshal` already ignores unknown keys, as exercised by the existing config sample's `token_stats` and `details` values; use the new test to confirm it also ignores a legacy `[widget]` table. Ensure `Save` omits the table. Keep the `Status.Theme` response field and set it to `"auto"` so `/api/status` retains its response shape.

- [ ] **Step 4: Remove widget references from the CLI**

Change `serve` help from “HTTP + widget” to “HTTP MCP server,” and remove the printed `widget:` URL. Add or update a CLI test to assert that serving still reports `MCP server:` and never prints `widget:`.

- [ ] **Step 5: Run focused server, config, and CLI tests**

Run: `go test ./internal/server ./internal/config ./internal/cli -count=1`
Expected: PASS; JSON endpoints and middleware remain operational, removed routes return 404, and legacy config loads cleanly.

### Task 3: Update project guidance and verify the complete removal

**Files:**
- Modify: `AGENTS.md`
- Modify: `README.md`
- Modify: `docs/CHATGPT.md`
- Modify: `docs/SECURITY.md`
- Modify: `docs/REFERENCES.md`
- Modify: `docs/superpowers/specs/2026-09-23-miodesk-maintenance-design.md`

**Interfaces:**
- Documentation must describe `status` as a native/text structured tool with no MCP App UI.
- Keep official references as historical protocol guidance only; do not document a miodesk widget as current behavior.

- [ ] **Step 1: Update user and contributor documentation**

Remove the widget directory and runtime UI requirements from `AGENTS.md`; remove the ChatGPT UI and `/widget` preview claims and the `[widget]` sample table from `README.md`; update `docs/CHATGPT.md` to describe status as text/structured output and remove the widget-error troubleshooting path. Update `docs/SECURITY.md` to remove the local-widget surface while preserving the documented protections for `/api/status` and `/api/edits`.

- [ ] **Step 2: Update protocol references and the current maintenance spec**

In `docs/REFERENCES.md`, retain the official optional-UI design rationale, but change miodesk's current decision to “no MCP Apps UI; native tools and structured status only.” Remove implementation details that claim current `ui://` resources, embedded HTML, or `/widget` preview. In the maintenance design's §3.3, replace widget-failure fallback with the approved no-widget status contract and preserve the existing `/api/edits` security requirements.

- [ ] **Step 3: Search for stale implementation references**

Run: `rg -n 'ui://miodesk|openai/outputTemplate|web/widget|/preview|/widget|MCP Apps widget|widget: %s|HTTP \+ widget|\[widget\]' --glob '!docs/superpowers/plans/2026-09-23-miodesk-context-status-contracts.md' --glob '!docs/superpowers/plans/2026-09-24-remove-mcp-apps-ui.md' .`
Expected: historical references remain only in the explicitly superseded 2026-09-23 plan and the MCP Apps protocol reference; no source, current CLI help, or current product documentation advertises miodesk UI behavior.

- [ ] **Step 4: Run repository verification and live MCP smoke**

Run: `go test -vet=all ./...`
Expected: PASS.

Run: `go vet -all ./... && go build -buildvcs=false ./cmd/miodesk && scripts/release-check.sh`
Expected: PASS with no widget assets required.

Start the built server on a temporary loopback port, call `/healthz`, `/api/status`, and `/mcp` with the official SDK client, verify status returns structured content and `resources/list` is empty, then stop the server cleanly. Run `git diff --check` and inspect `git status --short`; preserve unrelated pending files and do not commit or publish them.
