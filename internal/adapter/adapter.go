// Package adapter hosts client-facing UI metadata that must stay out of the
// core tools: the MCP Apps / ChatGPT widget (its ui:// resource, per-tool
// _meta.ui declarations, and the ChatGPT compatibility aliases) plus the
// invocation status labels ChatGPT displays. Core file/command tools know
// nothing about this package.
package adapter

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// WidgetURI is the shared result widget resource. The URI doubles as the
// host's cache key: bump the version segment whenever the embedded
// HTML/CSS/JS change in a user-visible way.
const WidgetURI = "ui://miodesk/status.html"

// WidgetMIMEType is the MCP Apps UI resource media type.
const WidgetMIMEType = "text/html;profile=mcp-app"

// Data returns a tool result payload for the widget to render. The shape is
// the tools' structuredContent (with its "kind" discriminator).
type Data func(context.Context) (any, error)

// resultToolNames are the tools whose results render in the shared widget,
// with their ≤64-char ChatGPT invocation labels.
var resultToolNames = map[string]struct{ invoking, invoked string }{
	"read":           {"Reading file…", "Read."},
	"search":         {"Searching workspace…", "Search complete."},
	"list":           {"Listing directory…", "Listed."},
	"write":          {"Writing file…", "Written."},
	"edit":           {"Editing file…", "Edited."},
	"delete":         {"Deleting…", "Deleted."},
	"command":        {"Running command…", "Command finished."},
	"command_start":  {"Starting command…", "Command started."},
	"command_poll":   {"Checking task…", "Task status."},
	"command_cancel": {"Cancelling task…", "Task cancelled."},
	"status":         {"Reading miodesk status…", "Status loaded."},
}

// ResultToolMeta returns the MCP Apps / ChatGPT _meta for a result-bearing
// tool: the shared widget resource plus invocation labels. Server applies it
// to each registered tool; core tools stay host-agnostic. Unknown names
// return nil.
func ResultToolMeta(name string) map[string]any {
	labels, ok := resultToolNames[name]
	if !ok {
		return nil
	}
	return map[string]any{
		"ui": map[string]any{
			"resourceUri": WidgetURI,
			"visibility":  []string{"model", "app"},
		},
		// ChatGPT compatibility alias for _meta.ui.resourceUri.
		"openai/outputTemplate":          WidgetURI,
		"openai/toolInvocation/invoking": labels.invoking,
		"openai/toolInvocation/invoked":  labels.invoked,
	}
}

// Attach registers the status tool and the widget resource on an MCP server.
// assets must expose the files under web/widget/static.
func Attach(s *mcp.Server, assets fs.FS, data Data) error {
	html, err := WidgetHTML(assets)
	if err != nil {
		return fmt.Errorf("assemble widget: %w", err)
	}

	s.AddTool(dashboardTool(), func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		out, err := data(ctx)
		if err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{StructuredContent: out}, nil
	})

	resource := &mcp.Resource{
		URI:         WidgetURI,
		Name:        "miodesk result view",
		Title:       "miodesk result view",
		Description: "Interactive view for miodesk tool results: file reads, searches, diffs, command output, and server status. Rendered as an MCP App.",
		MIMEType:    WidgetMIMEType,
	}
	resource.SetMeta(map[string]any{
		"ui": map[string]any{"prefersBorder": false},
	})
	s.AddResource(resource, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{
			Contents: []*mcp.ResourceContents{{
				URI:      WidgetURI,
				MIMEType: WidgetMIMEType,
				Text:     html,
				Meta:     map[string]any{"ui": map[string]any{"prefersBorder": false}},
			}},
		}, nil
	})
	return nil
}

// dashboardTool declares the status render tool with its ChatGPT/MCP Apps
// metadata and output schema.
func dashboardTool() *mcp.Tool {
	tool := &mcp.Tool{
		Name:        "status",
		Title:       "miodesk dashboard",
		Description: "Return miodesk server status for the diagnostics view: workspace root, endpoint, remote access mode, usage counters, and recent edits. Read-only.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "miodesk dashboard",
			ReadOnlyHint:    true,
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(false),
			IdempotentHint:  true,
		},
		InputSchema: emptyObjectSchema,
		OutputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":           map[string]any{"type": "string"},
				"version":        map[string]any{"type": "string"},
				"platform":       map[string]any{"type": "string"},
				"workspace":      map[string]any{"type": "string"},
				"endpoint":       map[string]any{"type": "string"},
				"port":           map[string]any{"type": "integer"},
				"theme":          map[string]any{"type": "string"},
				"remote":         map[string]any{"type": "string"},
				"started_at":     map[string]any{"type": "string"},
				"uptime_seconds": map[string]any{"type": "integer"},
				"stats":          map[string]any{"type": "object"},
				"tools":          map[string]any{"type": "array"},
				"edits":          map[string]any{"type": "array"},
			},
		},
	}
	tool.SetMeta(map[string]any{
		"ui": map[string]any{
			"resourceUri": WidgetURI,
			"visibility":  []string{"model", "app"},
		},
		// ChatGPT compatibility alias for _meta.ui.resourceUri.
		"openai/outputTemplate":          WidgetURI,
		"openai/toolInvocation/invoking": "Reading miodesk status…",
		"openai/toolInvocation/invoked":  "Status loaded.",
	})
	return tool
}

func boolPtr(b bool) *bool { return &b }

var emptyObjectSchema = map[string]any{
	"type":                 "object",
	"properties":           map[string]any{},
	"additionalProperties": false,
}

// WidgetHTML assembles the self-contained result widget. It backs the ui://
// resource and the /widget preview route.
func WidgetHTML(assets fs.FS) (string, error) { return widgetHTML(assets) }

// widgetHTML inlines the stylesheet, icons, renderers, and bootstrap script
// into the embed template. The result is self-contained: hosts may sandbox
// the page with no network access.
func widgetHTML(assets fs.FS) (string, error) {
	read := func(name string) (string, error) {
		b, err := fs.ReadFile(assets, name)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	page, err := read("static/embed.html")
	if err != nil {
		return "", err
	}
	parts := map[string]string{
		"/*__STYLE__*/":   "static/style.css",
		"//__ICONS__":     "static/icons.js",
		"//__RENDERERS__": "static/renderers.js",
		"//__SCRIPT__":    "static/app-embed.js",
	}
	html := page
	for marker, file := range parts {
		body, err := read(file)
		if err != nil {
			return "", err
		}
		html = strings.ReplaceAll(html, marker, body)
	}
	for marker := range parts {
		if strings.Contains(html, marker) {
			return "", errors.New("widget template marker was not replaced: " + marker)
		}
	}
	return html, nil
}

// PreviewHTML assembles the widget with the preview bootstrap and mock
// payloads inlined (used by the /preview development route only).
func PreviewHTML(assets fs.FS, kind string) (string, error) {
	html, err := widgetHTML(assets)
	if err != nil {
		return "", err
	}
	mocks, err := fs.ReadFile(assets, "static/mocks.js")
	if err != nil {
		return "", err
	}
	inject := "<script>" + string(mocks) + "</script>\n" +
		"<script>window.__MIODESK_PREVIEW = \"" + kind + "\";</script>\n"
	return strings.Replace(html, "</head>", inject+"</head>", 1), nil
}
