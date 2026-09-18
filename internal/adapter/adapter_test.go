package adapter

import (
	"strings"
	"testing"

	"github.com/Chillizu/miodesk/internal/buildinfo"
	widget "github.com/Chillizu/miodesk/web/widget"
)

func TestWidgetHTMLAssembly(t *testing.T) {
	html, err := widgetHTML(widget.Static)
	if err != nil {
		t.Fatalf("widgetHTML: %v", err)
	}
	for _, want := range []string{
		"<style>", "</style>", "<script>", "</script>",
		"MIODESK_ICONS",                // icon set present
		"MIODESK_RENDERERS",            // renderers present
		"bootWidget",                   // bootstrap present
		"ui/initialize",                // MCP Apps handshake present
		"appInfo",                      // MCP Apps view identity present
		"ui/notifications/tool-result", // standard result delivery present
		"--miodesk-text",               // design tokens present
		"code--lines",                  // source code owns its narrow scroll area
		"container-name: widget",       // outer layout uses container queries
		"white-space: pre-wrap",        // command output can wrap on narrow hosts
		"--miodesk-content-max",        // root and result share one width token
		"max-width: calc(var(--miodesk-content-max) + 2 * var(--miodesk-gap))",
		`["connection", connection]`, // compact status combines local access and tunnel provider
	} {
		if !strings.Contains(html, want) {
			t.Errorf("assembled widget missing %q", want)
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

func TestToolMetaKeepsOnlyStatusRich(t *testing.T) {
	for _, name := range []string{
		"read", "search", "list", "write", "edit", "delete", "command",
	} {
		if got := ToolMeta(name); got != nil {
			t.Errorf("native-first tool %q unexpectedly has client metadata: %v", name, got)
		}
	}

	for _, name := range []string{"command_start", "command_poll", "command_cancel"} {
		got := ToolMeta(name)
		if got == nil {
			t.Fatalf("command lifecycle tool %q has no invocation metadata", name)
		}
		if _, ok := got["ui"]; ok {
			t.Errorf("%q should stay in ChatGPT native UI: %v", name, got)
		}
		if _, ok := got["openai/outputTemplate"]; ok {
			t.Errorf("%q should not advertise a widget template: %v", name, got)
		}
		if got["openai/toolInvocation/invoking"] == nil || got["openai/toolInvocation/invoked"] == nil {
			t.Errorf("%q should keep lightweight invocation labels: %v", name, got)
		}
	}

	status := ToolMeta("status")
	ui, ok := status["ui"].(map[string]any)
	if !ok || ui["resourceUri"] != WidgetURI {
		t.Errorf("status ui metadata = %v", status["ui"])
	}
	if status["openai/outputTemplate"] != WidgetURI {
		t.Errorf("status ChatGPT template alias = %v", status["openai/outputTemplate"])
	}
}
