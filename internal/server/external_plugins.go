package server

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerExternalTools exposes only the names explicitly granted by a local
// manifest. The manifest, not remote self-reported MCP hints, decides permissions.
func registerExternalTools(s *Server) {
	if s.external == nil {
		return
	}
	for _, p := range s.external.Plugins {
		slog.Info("plugin_loaded", "plugin", p.ID, "tools", len(p.Tools))
	}
	for _, t := range s.external.Tools {
		t := t // capture the particular tool
		source := t.Source
		readonly := t.Grant.Permission == "read"
		destructive := !readonly
		open := false
		idempotent := readonly
		schema := source.InputSchema
		if schema == nil {
			schema = map[string]any{"type": "object"}
		}
		tool := &mcp.Tool{
			Name:        t.Name,
			Title:       source.Title,
			Description: fmt.Sprintf("External MCP plugin %s: %s", t.PluginID, source.Description),
			InputSchema: schema,
			Annotations: &mcp.ToolAnnotations{
				ReadOnlyHint: readonly, DestructiveHint: &destructive, OpenWorldHint: &open, IdempotentHint: idempotent,
			},
		}
		s.mcp.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			finish := s.beginTool(ctx, t.Name)
			var err error
			defer func() {
				if err != nil {
					finish(fmt.Errorf("plugin tool failed"))
				} else {
					finish(nil)
				}
			}()
			result, e := t.Call(ctx, req.Params.Arguments)
			if e != nil {
				err = e
				return nil, fmt.Errorf("plugin invocation failed (%s)", t.Name)
			}
			return result, nil
		})
		s.tools = append(s.tools, ToolInfo{Name: t.Name, Description: tool.Description})
	}
	// Inventory returns names and counts only; command paths/arguments and
	// credentials are deliberately excluded.
	mcp.AddTool(s.mcp, &mcp.Tool{
		Name: "plugins_status", Title: "External MCP plugins",
		Description: "List trusted, opted-in external MCP plugin IDs and exported tool names. Does not expose process commands or secrets.",
		Annotations: ann(true, false, false, true),
	}, func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, any, error) {
		done := s.beginTool(ctx, "plugins_status")
		defer done(nil)
		return nil, struct {
			Kind        string `json:"kind"`
			Plugins     any    `json:"plugins"`
			AllowWrites bool   `json:"allow_writes"`
		}{"plugins", s.external.Plugins, s.cfg.Plugins.AllowWrites}, nil
	})
	s.tools = append(s.tools, ToolInfo{Name: "plugins_status", Description: "List enabled external MCP plugin IDs and exported tools"})
}
