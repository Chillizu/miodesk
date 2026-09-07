package adapter

import (
	"strings"
	"testing"

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
	} {
		if !strings.Contains(html, want) {
			t.Errorf("assembled widget missing %q", want)
		}
	}
	if strings.Contains(html, "__STYLE__") || strings.Contains(html, "__SCRIPT__") ||
		strings.Contains(html, "__ICONS__") || strings.Contains(html, "__RENDERERS__") {
		t.Error("template markers must not survive assembly")
	}
	// Self-contained: no external URLs in resource/style/script references.
	if strings.Contains(html, `src="http`) || strings.Contains(html, `href="http`) {
		t.Error("widget must not reference external resources")
	}
}

func TestRichUIToolMetaNativeFirst(t *testing.T) {
	for _, name := range []string{
		"read", "search", "list", "write", "edit", "delete", "command",
	} {
		if got := RichUIToolMeta(name); got != nil {
			t.Errorf("native-first tool %q unexpectedly has UI metadata: %v", name, got)
		}
	}

	for _, name := range []string{"command_start", "command_poll", "command_cancel", "status"} {
		got := RichUIToolMeta(name)
		if got == nil {
			t.Errorf("rich UI tool %q has no UI metadata", name)
			continue
		}
		ui, ok := got["ui"].(map[string]any)
		if !ok || ui["resourceUri"] != WidgetURI {
			t.Errorf("%q ui metadata = %v", name, got["ui"])
		}
		if got["openai/outputTemplate"] != WidgetURI {
			t.Errorf("%q ChatGPT template alias = %v", name, got["openai/outputTemplate"])
		}
	}
}
