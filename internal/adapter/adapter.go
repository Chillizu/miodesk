// Package adapter hosts client-facing UI metadata that must stay out of the
// core tools: the MCP Apps / ChatGPT widget (its ui:// resource, per-tool
// _meta.ui declarations, and the ChatGPT compatibility aliases) plus the
// invocation status labels ChatGPT displays. Core file/command tools know
// nothing about this package.
package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/Chillizu/miodesk/internal/buildinfo"
)

// WidgetURI is the shared result widget resource. The URI doubles as the
// host's cache key: bump the version segment whenever the embedded
// HTML/CSS/JS change in a user-visible way.
const WidgetURI = "ui://miodesk/status-v7.html"

// legacyWidgetURIs keeps previously advertised template URIs readable while
// ChatGPT connector metadata catches up. The current tools always advertise
// WidgetURI; these aliases only prevent cached clients from getting a 400 when
// they fetch an older template.
var legacyWidgetURIs = []string{
	"ui://miodesk/status.html",
	"ui://miodesk/status-v2.html",
	"ui://miodesk/status-v3.html",
	"ui://miodesk/status-v4.html",
	"ui://miodesk/status-v5.html",
	"ui://miodesk/status-v6.html",
}

// WidgetMIMEType is the MCP Apps UI resource media type.
const WidgetMIMEType = "text/html;profile=mcp-app"

// Data returns a tool result payload for the widget to render. The shape is
// the tools' structuredContent (with its "kind" discriminator).
type Data func(context.Context) (any, error)

// invocationLabels are lightweight ChatGPT status strings. They do not imply
// a widget: long-running commands stay in ChatGPT's native tool UI, while only
// the diagnostics status tool opts into the embedded MCP App.
var invocationLabels = map[string]struct{ invoking, invoked string }{
	"command_start":  {"Starting command…", "Command started."},
	"command_poll":   {"Checking task…", "Task status."},
	"command_cancel": {"Cancelling task…", "Task cancelled."},
	"status":         {"Reading miodesk status…", "Status loaded."},
}

// ToolMeta returns client-facing metadata for one tool. Most tools need none;
// command lifecycle tools get only invocation labels, while status also gets
// the MCP Apps resource metadata.
func ToolMeta(name string) map[string]any {
	labels, ok := invocationLabels[name]
	if !ok {
		return nil
	}
	meta := map[string]any{
		"openai/toolInvocation/invoking": labels.invoking,
		"openai/toolInvocation/invoked":  labels.invoked,
	}
	if name == "status" {
		meta["ui"] = map[string]any{
			"resourceUri": WidgetURI,
			"visibility":  []string{"model", "app"},
		}
		// ChatGPT compatibility alias for _meta.ui.resourceUri.
		meta["openai/outputTemplate"] = WidgetURI
	}
	return meta
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

	registerWidget := func(uri string) {
		resource := &mcp.Resource{
			URI:         uri,
			Name:        "miodesk status view",
			Title:       "miodesk status",
			Description: "Compact miodesk connection and workspace diagnostics view. Rendered as an MCP App.",
			MIMEType:    WidgetMIMEType,
		}
		resource.SetMeta(map[string]any{
			"ui": map[string]any{"prefersBorder": true},
		})
		s.AddResource(resource, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{{
					URI:      uri,
					MIMEType: WidgetMIMEType,
					Text:     html,
					Meta:     map[string]any{"ui": map[string]any{"prefersBorder": true}},
				}},
			}, nil
		})
	}
	registerWidget(WidgetURI)
	for _, uri := range legacyWidgetURIs {
		registerWidget(uri)
	}
	return nil
}

// dashboardTool declares the status render tool with its ChatGPT/MCP Apps
// metadata and output schema.
func dashboardTool() *mcp.Tool {
	tool := &mcp.Tool{
		Name:        "status",
		Title:       "miodesk status",
		Description: "Return a compact diagnostics summary for the miodesk connection: workspace, endpoint, remote/tunnel mode, uptime, version, and total tool calls. Read-only.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "miodesk status",
			ReadOnlyHint:    true,
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(false),
			IdempotentHint:  true,
		},
		InputSchema: emptyObjectSchema,
		OutputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"kind":           map[string]any{"type": "string", "const": "status"},
				"name":           map[string]any{"type": "string"},
				"version":        map[string]any{"type": "string"},
				"platform":       map[string]any{"type": "string"},
				"workspace":      map[string]any{"type": "string"},
				"endpoint":       map[string]any{"type": "string"},
				"remote":         map[string]any{"type": "string"},
				"tunnel":         map[string]any{"type": "string"},
				"uptime_seconds": map[string]any{"type": "integer"},
				"tool_calls":     map[string]any{"type": "integer"},
			},
			"required": []string{
				"kind", "name", "version", "platform", "workspace", "endpoint",
				"remote", "tunnel", "uptime_seconds", "tool_calls",
			},
			"additionalProperties": false,
		},
	}
	tool.SetMeta(ToolMeta("status"))
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
	const versionMarker = "/*__MIODESK_APP_VERSION__*/"
	html = strings.ReplaceAll(html, versionMarker, string(mustJSON(buildinfo.Version)))
	for marker := range parts {
		if strings.Contains(html, marker) {
			return "", errors.New("widget template marker was not replaced: " + marker)
		}
	}
	if strings.Contains(html, versionMarker) {
		return "", errors.New("widget app version marker was not replaced")
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
		"<script>window.__MIODESK_PREVIEW = " + string(mustJSON(kind)) + ";</script>\n"
	return strings.Replace(html, "</head>", inject+"</head>", 1), nil
}

func mustJSON(v string) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte(`""`)
	}
	return b
}
