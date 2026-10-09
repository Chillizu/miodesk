package adapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRegisterStatusWithoutAppsUI(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "adapter-test", Version: "test"}, nil)
	RegisterStatus(server, func(context.Context) (any, error) {
		return map[string]any{"kind": "status", "source": "test handler"}, nil
	})

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless:    true,
		JSONResponse: true,
	})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "adapter-test-client", Version: "test"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             ts.URL,
		HTTPClient:           ts.Client(),
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer session.Close()

	listed, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	if len(listed.Tools) != 1 || listed.Tools[0].Name != "status" {
		t.Fatalf("tools/list = %+v, want only status", listed.Tools)
	}
	tool := listed.Tools[0]
	if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint ||
		tool.Annotations.DestructiveHint == nil || *tool.Annotations.DestructiveHint ||
		tool.Annotations.OpenWorldHint == nil || *tool.Annotations.OpenWorldHint ||
		!tool.Annotations.IdempotentHint {
		t.Errorf("status annotations = %+v", tool.Annotations)
	}
	if tool.InputSchema == nil || tool.OutputSchema == nil {
		t.Errorf("status schemas = input %#v, output %#v", tool.InputSchema, tool.OutputSchema)
	}
	meta := listed.Tools[0].Meta
	if _, ok := meta["ui"]; ok {
		t.Errorf("status must not reference a missing UI resource: %v", meta)
	}
	if _, ok := meta["openai/outputTemplate"]; ok {
		t.Errorf("status must not reference a missing output template: %v", meta)
	}
	if meta["openai/toolInvocation/invoking"] == nil || meta["openai/toolInvocation/invoked"] == nil {
		t.Errorf("text status should retain invocation labels: %v", meta)
	}
	resources, err := session.ListResources(ctx, &mcp.ListResourcesParams{})
	if err != nil {
		t.Fatalf("resources/list: %v", err)
	}
	if len(resources.Resources) != 0 {
		t.Errorf("resources/list = %+v, want no resources", resources.Resources)
	}

	call, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "status", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("call status: %v", err)
	}
	if call.IsError {
		t.Fatalf("status returned an error: %v", call.Content)
	}
	data, err := json.Marshal(call.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("status structured content: %v (%s)", err, data)
	}
	if payload["kind"] != "status" || payload["source"] != "test handler" {
		t.Errorf("status payload = %v", payload)
	}
}

func TestToolMetaKeepsStatusRichAndExecNative(t *testing.T) {
	for _, name := range []string{
		"read", "search", "list", "write", "edit", "delete",
	} {
		if got := ToolMeta(name); got != nil {
			t.Errorf("native-first tool %q unexpectedly has client metadata: %v", name, got)
		}
	}

	for _, name := range []string{"exec_command", "write_stdin"} {
		got := ToolMeta(name)
		if got == nil {
			t.Fatalf("%q should keep lightweight invocation metadata", name)
		}
		if _, ok := got["ui"]; ok {
			t.Errorf("%q should not advertise embedded UI: %v", name, got)
		}
		if _, ok := got["openai/outputTemplate"]; ok {
			t.Errorf("%q should not advertise an output template: %v", name, got)
		}
		if got["openai/toolInvocation/invoking"] == nil || got["openai/toolInvocation/invoked"] == nil {
			t.Errorf("%q should keep lightweight invocation labels: %v", name, got)
		}
	}

	status := ToolMeta("status")
	if status["openai/toolInvocation/invoking"] == nil || status["openai/toolInvocation/invoked"] == nil {
		t.Errorf("status should retain invocation labels: %v", status)
	}
	if _, ok := status["ui"]; ok {
		t.Errorf("status must not advertise UI: %v", status)
	}
	if _, ok := status["openai/outputTemplate"]; ok {
		t.Errorf("status must not advertise a template: %v", status)
	}
}
