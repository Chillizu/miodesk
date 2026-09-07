package adapter

import (
	"strings"
	"testing"

	widget "miodesk/web/widget"
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
