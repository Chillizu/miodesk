// Package adapter keeps client-facing invocation labels out of the core tools
// and registers the read-only status tool.
package adapter

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Data returns the structured status tool result.
type Data func(context.Context) (any, error)

var invocationLabels = map[string]struct{ invoking, invoked string }{
	"exec_command": {"Running command…", "Command checked."},
	"write_stdin":  {"Resuming command…", "Command checked."},
	"status":       {"Reading miodesk status…", "Status loaded."},
}

// ToolMeta returns the lightweight invocation labels for supported tools.
func ToolMeta(name string) map[string]any { return invocationMeta(name) }

func invocationMeta(name string) map[string]any {
	labels, ok := invocationLabels[name]
	if !ok {
		return nil
	}
	return map[string]any{
		"openai/toolInvocation/invoking": labels.invoking,
		"openai/toolInvocation/invoked":  labels.invoked,
	}
}

// RegisterStatus adds the native, read-only structured diagnostics tool.
func RegisterStatus(s *mcp.Server, data Data) {
	s.AddTool(statusTool(), func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		out, err := data(ctx)
		if err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{StructuredContent: out}, nil
	})
}

func statusTool() *mcp.Tool {
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
	tool.SetMeta(invocationMeta("status"))
	return tool
}

func boolPtr(b bool) *bool { return &b }

var emptyObjectSchema = map[string]any{
	"type":                 "object",
	"properties":           map[string]any{},
	"additionalProperties": false,
}
