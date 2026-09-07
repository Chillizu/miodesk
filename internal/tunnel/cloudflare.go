package tunnel

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"sync"
	"time"
)

// Cloudflare runs a zero-config quick tunnel via the cloudflared binary:
// `cloudflared tunnel --url <local>` prints a random
// https://<name>.trycloudflare.com URL. No account needed; documented limits
// are 200 concurrent requests and no SSE (quick tunnels are for testing).
type Cloudflare struct {
	mu   sync.Mutex
	cmd  *exec.Cmd
	out  bytes.Buffer
	done chan struct{}
}

func (c *Cloudflare) Name() string { return "cloudflare" }

func (c *Cloudflare) Available() error {
	if _, err := lookPath("cloudflared"); err != nil {
		return fmt.Errorf("cloudflared not found")
	}
	return nil
}

// cloudflaredURL matches the quick-tunnel URL cloudflared prints.
var cloudflaredURL = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

func (c *Cloudflare) Start(ctx context.Context, opts Options) (Endpoint, error) {
	cmd := exec.CommandContext(ctx, "cloudflared", "tunnel", "--url", opts.LocalURL)
	// Bound the pipe drain: a killed process's grandchildren can hold the
	// stdout pipe open for a long time, which would stall Wait forever.
	cmd.WaitDelay = 3 * time.Second
	c.mu.Lock()
	c.out.Reset()
	c.cmd = cmd
	c.done = make(chan struct{})
	out := &c.out
	done := c.done
	c.mu.Unlock()

	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		close(done)
		return Endpoint{}, fmt.Errorf("start cloudflared: %w", err)
	}
	go func() { _ = cmd.Wait(); close(done) }()

	deadline := time.After(time.Duration(Timeout(opts.TimeoutSeconds)) * time.Second)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-deadline:
			c.Stop()
			return Endpoint{}, fmt.Errorf("cloudflared did not report a trycloudflare URL within %ds (run with MIODESK_LOG=debug to inspect its output)", Timeout(opts.TimeoutSeconds))
		case <-done:
			return Endpoint{}, fmt.Errorf("cloudflared exited before creating the tunnel: %s", lastLines(out.String(), 3))
		case <-tick.C:
			if u := parseCloudflaredURL(out.String()); u != "" {
				return Endpoint{Provider: "cloudflare", URL: u}, nil
			}
		}
	}
}

// Stop kills the cloudflared process.
func (c *Cloudflare) Stop() error {
	c.mu.Lock()
	cmd := c.cmd
	done := c.done
	c.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	select {
	case <-done:
		return nil
	default:
		_ = cmd.Process.Kill()
		<-done
	}
	return nil
}

func lastLines(s string, n int) string {
	lines := nonEmptyLines(s)
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return joinLines(lines)
}
