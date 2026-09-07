package tunnel

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Tailscale exposes miodesk with `tailscale funnel --bg localhost:<port>`.
// Funnel availability depends on the tailnet (HTTPS enabled, ACLs) and the
// CLI syntax needs tailscale >= 1.52, so everything is probed at runtime.
// The public URL is read from `tailscale funnel status`.
type Tailscale struct {
	mu   sync.Mutex
	cmd  *exec.Cmd
	out  bytes.Buffer
	port string
	done chan struct{}
}

func (t *Tailscale) Name() string { return "tailscale" }

func (t *Tailscale) Available() error {
	if _, err := lookPath("tailscale"); err != nil {
		return fmt.Errorf("tailscale not found")
	}
	// `funnel status` fails when tailscaled is not running or the version
	// predates funnel support; either way this provider is unusable now.
	if err := exec.Command("tailscale", "funnel", "status").Run(); err != nil {
		return fmt.Errorf("funnel not available on this tailnet (needs HTTPS enabled and tailscale >= 1.52)")
	}
	return nil
}

func (t *Tailscale) Start(ctx context.Context, opts Options) (Endpoint, error) {
	port, err := localPort(opts.LocalURL)
	if err != nil {
		return Endpoint{}, err
	}

	// --bg lets tailscaled own the funnel; miodesk tears it down in Stop.
	config := exec.CommandContext(ctx, "tailscale", "funnel", "--bg", "localhost:"+port)
	var cfgErr bytes.Buffer
	config.Stderr = &cfgErr
	if err := config.Run(); err != nil {
		return Endpoint{}, fmt.Errorf("tailscale funnel --bg: %s", strings.TrimSpace(firstNonEmpty(cfgErr.String(), err.Error())))
	}

	t.mu.Lock()
	t.port = port
	t.out.Reset()
	t.done = make(chan struct{})
	t.mu.Unlock()

	deadline := time.After(time.Duration(Timeout(opts.TimeoutSeconds)) * time.Second)
	tick := time.NewTicker(400 * time.Millisecond)
	for {
		select {
		case <-deadline:
			t.Stop()
			return Endpoint{}, fmt.Errorf("tailscale funnel status did not show a URL within %ds", Timeout(opts.TimeoutSeconds))
		case <-tick.C:
			out, err := exec.Command("tailscale", "funnel", "status").Output()
			if err != nil {
				continue
			}
			if u := parseTailscaleStatus(string(out)); u != "" {
				return Endpoint{Provider: "tailscale", URL: u}, nil
			}
		}
	}
}

// Stop disables the funnel miodesk created. A scoped off is attempted first;
// `tailscale funnel reset` would wipe the user's other funnels, so it is only
// ever suggested, never run.
func (t *Tailscale) Stop() error {
	t.mu.Lock()
	port := t.port
	t.mu.Unlock()
	if port == "" {
		return nil
	}
	if err := exec.Command("tailscale", "funnel", "localhost:"+port, "off").Run(); err != nil {
		return fmt.Errorf("could not disable the funnel: run `tailscale funnel reset` manually")
	}
	t.mu.Lock()
	t.port = ""
	t.mu.Unlock()
	return nil
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
