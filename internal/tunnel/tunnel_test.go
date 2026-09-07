package tunnel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseCloudflaredURL(t *testing.T) {
	out := `2026-09-06T01:00:00Z INF +--------------------------------------------------------------------+
2026-09-06T01:00:00Z INF |  Your quick Tunnel has been created! Visit it at (it may take some time to be reachable):  |
2026-09-06T01:00:00Z INF |  https://dizzy-monkey-see-you.trycloudflare.com                                     |
2026-09-06T01:00:00Z INF +--------------------------------------------------------------------+`
	if got := parseCloudflaredURL(out); got != "https://dizzy-monkey-see-you.trycloudflare.com" {
		t.Errorf("parseCloudflaredURL = %q", got)
	}
	if got := parseCloudflaredURL("no url here"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestParseNgrokTunnels(t *testing.T) {
	data := []byte(`{"tunnels":[
		{"public_url":"http://abc123.ngrok-free.app"},
		{"public_url":"https://abc123.ngrok-free.app"}
	]}`)
	if got := parseNgrokTunnels(data); got != "https://abc123.ngrok-free.app" {
		t.Errorf("https should be preferred, got %q", got)
	}
	data = []byte(`{"tunnels":[{"public_url":"http://only-http.ngrok-free.app"}]}`)
	if got := parseNgrokTunnels(data); got != "http://only-http.ngrok-free.app" {
		t.Errorf("http fallback = %q", got)
	}
	if got := parseNgrokTunnels([]byte("not json")); got != "" {
		t.Errorf("invalid json should yield empty, got %q", got)
	}
	if got := parseNgrokTunnels([]byte(`{"tunnels":[]}`)); got != "" {
		t.Errorf("empty tunnels should yield empty, got %q", got)
	}
}

func TestParseTailscaleStatus(t *testing.T) {
	out := `funnel:
|-- tcp://my-machine.tail-scale.ts.net:443 (TLS terminated)
|-- http://my-machine.tail-scale.ts.net:80 (proxying http://127.0.0.1:8791)`
	if got := parseTailscaleStatus(out); got != "https://my-machine.tail-scale.ts.net" {
		t.Errorf("tcp form = %q", got)
	}
	out = "funnel:\n|-- https://my-machine.tail-scale.ts.net"
	if got := parseTailscaleStatus(out); got != "https://my-machine.tail-scale.ts.net" {
		t.Errorf("https form = %q", got)
	}
	if got := parseTailscaleStatus("nothing"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestCustomValidation(t *testing.T) {
	c := Custom{}
	ctx := context.Background()
	if _, err := c.Start(ctx, Options{}); err == nil {
		t.Error("missing URL must error")
	}
	for _, bad := range []string{"ftp://x", "not a url", "https://"} {
		if _, err := c.Start(ctx, Options{CustomURL: bad}); err == nil {
			t.Errorf("invalid custom URL %q must error", bad)
		}
	}
	ep, err := c.Start(ctx, Options{CustomURL: "https://demo.example.com/"})
	if err != nil {
		t.Fatalf("valid custom URL: %v", err)
	}
	if ep.URL != "https://demo.example.com" || ep.Provider != "custom" {
		t.Errorf("endpoint = %+v", ep)
	}
	if ep.MCPURL() != "https://demo.example.com/mcp" {
		t.Errorf("MCPURL = %q", ep.MCPURL())
	}
}

func TestLocalProvider(t *testing.T) {
	ep, err := Local{}.Start(context.Background(), Options{LocalURL: "http://127.0.0.1:1234"})
	if err != nil {
		t.Fatal(err)
	}
	if ep.Provider != "local" || ep.URL != "http://127.0.0.1:1234" {
		t.Errorf("endpoint = %+v", ep)
	}
	if err := (Local{}).Stop(); err != nil {
		t.Error("local Stop should be a no-op")
	}
}

func TestDetectAllShape(t *testing.T) {
	// lookPath is stubbed so the test does not depend on the host's binaries.
	restore := stubLookPath(t, map[string]string{})
	defer restore()

	all := DetectAll()
	if len(all) != 5 {
		t.Fatalf("detections = %d, want 5", len(all))
	}
	names := map[string]bool{}
	for _, d := range all {
		names[d.Name] = true
	}
	for _, want := range []string{"local", "custom", "cloudflare", "ngrok", "tailscale"} {
		if !names[want] {
			t.Errorf("missing detection for %q: %v", want, all)
		}
	}
	for _, d := range all {
		if d.Name == "local" || d.Name == "custom" {
			if !d.Available {
				t.Errorf("%s must always be available", d.Name)
			}
		} else if d.Available {
			t.Errorf("%s must be unavailable without binaries", d.Name)
		} else if d.Reason == "" {
			t.Errorf("%s must explain why it is unavailable", d.Name)
		}
	}
}

func TestDetectAutoDegradesGracefully(t *testing.T) {
	restore := stubLookPath(t, map[string]string{})
	defer restore()

	_, err := Detect("auto")
	if err == nil {
		t.Fatal("auto without any binary must fail")
	}
	var autoErr *AutoDetectError
	if !errors.As(err, &autoErr) {
		t.Fatalf("error = %v, want *AutoDetectError", err)
	}
	// The message names every provider and its reason.
	for _, name := range []string{"cloudflare", "ngrok", "tailscale"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error should mention %s: %v", name, err)
		}
	}
	if h := autoErr.Hint(); !strings.Contains(h, "--provider custom") {
		t.Errorf("hint should offer the custom escape hatch: %q", h)
	}
}

func TestDetectExplicitUnavailable(t *testing.T) {
	restore := stubLookPath(t, map[string]string{})
	defer restore()

	_, err := Detect("cloudflare")
	if err == nil {
		t.Fatal("explicit unavailable provider must fail")
	}
	if !strings.Contains(err.Error(), "cloudflare") || !strings.Contains(err.Error(), "not available") {
		t.Errorf("error = %v", err)
	}
	var ue interface{ Hint() string }
	if errors.As(err, &ue) && !strings.Contains(ue.Hint(), "custom --url") {
		t.Errorf("hint should suggest custom: %q", ue.Hint())
	}
}

func TestDetectExplicitAvailable(t *testing.T) {
	stub := writeStub(t, "cloudflared", "#!/bin/sh\nsleep 300\n")
	restore := stubLookPath(t, map[string]string{"cloudflared": stub})
	defer restore()

	p, err := Detect("cloudflare")
	if err != nil {
		t.Fatalf("Detect(cloudflare): %v", err)
	}
	if p.Name() != "cloudflare" {
		t.Errorf("name = %q", p.Name())
	}
	// Auto picks the first available network provider in preference order.
	p, err = Detect("auto")
	if err != nil {
		t.Fatalf("Detect(auto): %v", err)
	}
	if p.Name() != "cloudflare" {
		t.Errorf("auto should prefer the first available provider, got %q", p.Name())
	}
}

func TestCloudflareStartWithStub(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "cloudflared")
	body := "#!/bin/sh\n" +
		`echo "2026-09-06T01:00:00Z INF |  Your quick Tunnel has been created! |"` + "\n" +
		`echo "2026-09-06T01:00:00Z INF |  https://stub-tunnel-demo.trycloudflare.com |"` + "\n" +
		"sleep 300\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	// The stub dir shadows the real cloudflared, but the rest of PATH stays
	// available so the stub's own commands still resolve.
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	c := &Cloudflare{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ep, err := c.Start(ctx, Options{LocalURL: "http://127.0.0.1:8791", TimeoutSeconds: 5})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if ep.URL != "https://stub-tunnel-demo.trycloudflare.com" || ep.Provider != "cloudflare" {
		t.Errorf("endpoint = %+v", ep)
	}

	// Stop kills the stub (its sleep keeps it alive otherwise).
	if err := c.Stop(); err != nil {
		t.Errorf("Stop: %v", err)
	}
	select {
	case <-c.done:
	default:
		t.Error("process should be reaped after Stop")
	}
}

func TestCloudflareStartFailureSurfacesOutput(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "cloudflared")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho 'INF some failure happened' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	c := &Cloudflare{}
	_, err := c.Start(context.Background(), Options{LocalURL: "http://127.0.0.1:1", TimeoutSeconds: 5})
	if err == nil {
		t.Fatal("exiting stub must fail Start")
	}
	if !strings.Contains(err.Error(), "exited before creating the tunnel") {
		t.Errorf("error = %v", err)
	}
}

func TestTimeoutNormalization(t *testing.T) {
	for _, tc := range []struct{ in, want int }{
		{0, 30}, {-5, 30}, {10, 10}, {1000, 300},
	} {
		if got := Timeout(tc.in); got != tc.want {
			t.Errorf("Timeout(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
	if time.Duration(Timeout(0))*time.Second != 30*time.Second {
		t.Error("default timeout should be 30s")
	}
}

// stubLookPath replaces the package's binary lookup with a table. A name not
// in the table reports "not found". The returned func restores the original.
func stubLookPath(t *testing.T, stubs map[string]string) func() {
	t.Helper()
	orig := lookPath
	lookPath = func(name string) (string, error) {
		if p, ok := stubs[name]; ok {
			return p, nil
		}
		return "", os.ErrNotExist
	}
	return func() { lookPath = orig }
}

func writeStub(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}
