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

	status := ToolMeta("status")
	ui, ok := status["ui"].(map[string]any)
	if !ok || ui["resourceUri"] != WidgetURI {
		t.Errorf("status ui metadata = %v", status["ui"])
	}
	if status["openai/outputTemplate"] != WidgetURI {
		t.Errorf("status ChatGPT template alias = %v", status["openai/outputTemplate"])
	}
}
