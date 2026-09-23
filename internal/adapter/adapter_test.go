package adapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Chillizu/miodesk/internal/buildinfo"
	widget "github.com/Chillizu/miodesk/web/widget"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAttachRegistersTextStatusWhenWidgetAssemblyFails(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "adapter-test", Version: "test"}, nil)
	attachErr := Attach(server, fstest.MapFS{}, func(context.Context) (any, error) {
		return map[string]any{"kind": "status", "source": "test handler"}, nil
	})
	if attachErr == nil {
		t.Fatal("Attach should report missing widget assets")
	}

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
		t.Errorf("resources/list = %+v, want no widget resources", resources.Resources)
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

func TestWidgetHTMLAssembly(t *testing.T) {
	html, err := widgetHTML(widget.Static)
	if err != nil {
		t.Fatalf("widgetHTML: %v", err)
	}
	for _, want := range []string{
		"<style>", "</style>", "<script>", "</script>",
		"MIODESK_ICONS",                // icon set present
		"renderStatus",                 // diagnostics renderer present
		"bootWidget",                   // bootstrap present
		"ui/initialize",                // MCP Apps handshake present
		"appInfo",                      // MCP Apps view identity present
		"ui/notifications/tool-result", // standard result delivery present
		"--miodesk-text",               // host-native design tokens present
		"container-name: widget",       // narrow-host adaptation remains
		"--miodesk-content-max",
		"max-width: calc(var(--miodesk-content-max) + 2 * var(--miodesk-gap))",
		`["connection", connection]`, // compact status combines local access and tunnel provider
	} {
		if !strings.Contains(html, want) {
			t.Errorf("assembled widget missing %q", want)
		}
	}
	for _, retired := range []string{`method: "tools/call"`, `command_poll`, `cancelTask`, `"Task"`} {
		if strings.Contains(html, retired) {
			t.Errorf("diagnostics-only widget still contains retired task UI %q", retired)
		}
	}
	if strings.Contains(html, "__STYLE__") || strings.Contains(html, "__SCRIPT__") ||
		strings.Contains(html, "__ICONS__") || strings.Contains(html, "__RENDERERS__") ||
		strings.Contains(html, "__MIODESK_APP_VERSION__") {
		t.Error("template markers must not survive assembly")
	}
	if !strings.Contains(html, `appInfo: { name: "miodesk", version: "`+buildinfo.Version+`" }`) {
		t.Error("widget appInfo should use the binary build version")
	}
	mocks, err := widget.Static.ReadFile("static/mocks.js")
	if err != nil {
		t.Fatalf("read mocks: %v", err)
	}
	if !strings.Contains(string(mocks), `tunnel: "openai"`) {
		t.Error("status preview mock should include the OpenAI tunnel provider")
	}

	// Self-contained: no external URLs in resource/style/script references.
	if strings.Contains(html, `src="http`) || strings.Contains(html, `href="http`) {
		t.Error("widget must not reference external resources")
	}
}

func TestWidgetMetaUsesExplicitDenyByDefaultCSP(t *testing.T) {
	meta := widgetMeta()
	ui, ok := meta["ui"].(map[string]any)
	if !ok {
		t.Fatalf("widget ui metadata = %v", meta)
	}
	if ui["prefersBorder"] != true {
		t.Errorf("prefersBorder = %v", ui["prefersBorder"])
	}
	csp, ok := ui["csp"].(map[string]any)
	if !ok {
		t.Fatalf("widget csp metadata = %v", ui)
	}
	for _, key := range []string{"connectDomains", "resourceDomains"} {
		domains, ok := csp[key].([]string)
		if !ok || len(domains) != 0 {
			t.Errorf("%s = %#v, want explicit empty allowlist", key, csp[key])
		}
	}
	if _, ok := csp["frameDomains"]; ok {
		t.Errorf("frameDomains should stay omitted for diagnostics-only UI: %v", csp)
	}
	if _, ok := meta["openai/widgetCSP"]; ok {
		t.Errorf("legacy widgetCSP should not be emitted without redirect_domains: %v", meta)
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
			t.Errorf("%q should not advertise a widget template: %v", name, got)
		}
		if got["openai/toolInvocation/invoking"] == nil || got["openai/toolInvocation/invoked"] == nil {
			t.Errorf("%q should keep lightweight invocation labels: %v", name, got)
		}
	}

	plainStatus := StatusToolMeta(false)
	if _, ok := plainStatus["ui"]; ok {
		t.Errorf("status without a widget must not advertise ui metadata: %v", plainStatus)
	}
	if _, ok := plainStatus["openai/outputTemplate"]; ok {
		t.Errorf("status without a widget must not advertise an output template: %v", plainStatus)
	}
	if plainStatus["openai/toolInvocation/invoking"] == nil || plainStatus["openai/toolInvocation/invoked"] == nil {
		t.Errorf("status without a widget should retain invocation labels: %v", plainStatus)
	}

	status := StatusToolMeta(true)
	ui, ok := status["ui"].(map[string]any)
	if !ok || ui["resourceUri"] != WidgetURI {
		t.Errorf("status ui metadata = %v", status["ui"])
	}
	if status["openai/outputTemplate"] != WidgetURI {
		t.Errorf("status ChatGPT template alias = %v", status["openai/outputTemplate"])
	}
}
