/* Preview-only mock payloads (inlined into /preview, never shipped data).
   Key = renderer kind; "-fail"/"-timeout"/"-cancel" variants exist for the
   command lifecycle. */

"use strict";

window.MIODESK_MOCKS = {
  read: {
    kind: "read", path: "internal/server/server.go", size: 15340, offset: 40, lines: 14, truncated: false,
    content: "// Package server wires miodesk's MCP server, widget, and status API\ntype Server struct {\n\tcfg     *config.Config\n\tws      *workspace.Workspace\n\tmcp     *mcp.Server\n\tstarted time.Time\n}\n\n// New builds a Server with all tools registered.\nfunc New(cfg *config.Config, ws *workspace.Workspace) *Server {\n\ts := &Server{cfg: cfg, ws: ws, started: time.Now()}\n\ts.mcp = mcp.NewServer(\u0026mcp.Implementation{Name: \"miodesk\"}, nil)\n\tregisterTools(s)\n\treturn s\n}",
  },
  "read-truncated": {
    kind: "read", path: "logs/app-huge.log", size: 10485760, offset: 1, lines: 6, truncated: true,
    content: "2026-09-06T10:00:00Z INF request received\n2026-09-06T10:00:00Z INF request received\n2026-09-06T10:00:01Z INF request received\n2026-09-06T10:00:01Z INF request received\n2026-09-06T10:00:02Z INF request received\n2026-09-06T10:00:02Z INF request received",
  },
  search: {
    kind: "search", query: "workspace", engine: "ripgrep", truncated: false, matches: [
      { file: "internal/server/server.go", line: 14, text: "// Package server wires miodesk's workspace bridge" },
      { file: "internal/server/server.go", line: 27, text: "ws      *workspace.Workspace" },
      { file: "internal/server/server.go", line: 51, text: "func New(cfg *config.Config, ws *workspace.Workspace) *Server {" },
      { file: "internal/tools/read.go", line: 12, text: "ws *workspace.Workspace, in ReadInput" },
      { file: "internal/tools/edit.go", line: 9, text: "// workspace root. Paths are validated on canonical," },
      { file: "README.md", line: 4, text: "safely access your workspace, files, and dev tools" },
    ],
  },
  "search-empty": { kind: "search", query: "zzz-not-found", engine: "builtin", truncated: false, matches: [] },
  list: {
    kind: "list", path: ".", truncated: false, entries: [
      { name: "cmd", path: "cmd", kind: "dir" },
      { name: "miodesk", path: "cmd/miodesk", kind: "dir" },
      { name: "main.go", path: "cmd/miodesk/main.go", kind: "file", size: 178 },
      { name: "internal", path: "internal", kind: "dir" },
      { name: "go.mod", path: "go.mod", kind: "file", size: 421 },
      { name: "go.sum", path: "go.sum", kind: "file", size: 3120 },
      { name: "link-to-docs", path: "link-to-docs", kind: "symlink" },
    ],
  },
  write: { kind: "write", path: "notes/idea.md", bytes: 128, created: true },
  "write-overwrite": { kind: "write", path: "notes/idea.md", bytes: 96, created: false },
  delete: { kind: "delete", path: "tmp/scratch.txt", target: "file" },
  edit: {
    kind: "edit", applied: 2, files: [{
      path: "internal/tools/read.go", bytes: 2140, lines: 130, diff: [
        { header: "@@ -12,7 +12,9 @@", lines: [
          { type: "ctx", old: 12, new: 12, text: "const (" },
          { type: "del", old: 13, text: "\tMaxReadBytes = 256 \u003c\u003c 10" },
          { type: "add", new: 13, text: "\tMaxReadBytes = 512 \u003c\u003c 10" },
          { type: "ctx", old: 14, new: 14, text: "\tDefaultReadLines = 2000" },
          { type: "add", new: 15, text: "\tDefaultOffset     = 1" },
          { type: "ctx", old: 15, new: 16, text: ")" },
        ]},
        { header: "@@ -88,6 +90,8 @@", lines: [
          { type: "ctx", old: 88, new: 90, text: "\tdefer f.Close()" },
          { type: "del", old: 89, text: "\tif fi.Size() \u003e MaxReadBytes {" },
          { type: "del", old: 90, text: "\t\treturn nil, errTooLarge" },
          { type: "add", new: 91, text: "\t// Read one extra byte to detect truncation." },
          { type: "add", new: 92, text: "\tbuf := make([]byte, MaxReadBytes+1)" },
          { type: "ctx", old: 91, new: 93, text: "\tn, err := f.Read(buf)" },
        ]},
      ],
    }],
  },
  command: {
    kind: "command", command: "go test ./internal/tools/", exit_code: 0, elapsed_ms: 2140,
    stdout: "ok  \tmiodesk/internal/tools\t1.842s\n?   \tmiodesk/internal/xdg\t[no test files]",
    stderr: "", stdout_truncated: false, stderr_truncated: false, timed_out: false,
  },
  "command-fail": {
    kind: "command", command: "go vet ./...", exit_code: 1, elapsed_ms: 640,
    stdout: "", stderr: "# miodesk/internal/server\ninternal/server/server.go:55:2: undefined: authX\n", timed_out: false,
  },
  "command-timeout": {
    kind: "command", command: "sleep 600", exit_code: -1, elapsed_ms: 120000, stdout: "", stderr: "", timed_out: true,
  },
  "command-cancel": {
    kind: "command", command: "npm run build", exit_code: -1, elapsed_ms: 4200, stdout: "building…\n", stderr: "", timed_out: false,
  },
  "command-start": { kind: "task", id: "task-3", label: "Run development server", status: "running" },
  "command-poll": {
    kind: "task", id: "task-3", label: "Run development server", status: "running",
    exit_code: null, elapsed_ms: 42000, stdout: "watching for changes…\nrebuilding on save\n", stderr: "", timed_out: false,
  },
  status: {
    kind: "status", name: "miodesk", version: "0.3.0", platform: "linux/amd64",
    workspace: "/home/you/project", endpoint: "http://127.0.0.1:8791/mcp",
    remote: "local", tunnel: "openai", port: 8791, uptime_seconds: 3721,
    stats: { calls: { read: 12, search: 4, edit: 2, command: 9 }, total: 27 },
  },
};
