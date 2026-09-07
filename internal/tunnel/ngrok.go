package tunnel

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"sync"
	"time"
)

// Ngrok runs `ngrok http <port>` and reads the public URL from the agent's
// local web interface/API (web_addr, default 127.0.0.1:4040). miodesk binds
// web_addr to a private port so it never collides with another ngrok. An
// authtoken is required by ngrok itself; `ngrok config check` is the probe.
type Ngrok struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	apiURL string
	done   chan struct{}
}

func (n *Ngrok) Name() string { return "ngrok" }

func (n *Ngrok) Available() error {
	if _, err := lookPath("ngrok"); err != nil {
		return fmt.Errorf("ngrok not found")
	}
	if err := exec.Command("ngrok", "config", "check").Run(); err != nil {
		return fmt.Errorf("authtoken not configured (run: ngrok config add-authtoken <token>)")
	}
	return nil
}

func (n *Ngrok) Start(ctx context.Context, opts Options) (Endpoint, error) {
	port, err := localPort(opts.LocalURL)
	if err != nil {
		return Endpoint{}, err
	}
	apiPort, err := freePort()
	if err != nil {
		return Endpoint{}, fmt.Errorf("reserve web-addr port: %w", err)
	}
	// Flags mirror ngrok's documented config keys (log, log_format, web_addr).
	cmd := exec.CommandContext(ctx, "ngrok", "http", port,
		"--log", "stdout", "--log-format", "json",
		"--web-addr", "127.0.0.1:"+portString(apiPort))
	cmd.WaitDelay = 3 * time.Second

	n.mu.Lock()
	n.cmd = cmd
	n.apiURL = fmt.Sprintf("http://127.0.0.1:%s", portString(apiPort))
	n.done = make(chan struct{})
	done := n.done
	n.mu.Unlock()

	cmd.Stdout = nil // logs would be noise; the API is the source of truth
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		close(done)
		return Endpoint{}, fmt.Errorf("start ngrok: %w", err)
	}
	go func() { _ = cmd.Wait(); close(done) }()

	endpoint, err := n.awaitURL(ctx, done, Timeout(opts.TimeoutSeconds))
	if err != nil {
		n.Stop()
		return Endpoint{}, err
	}
	return endpoint, nil
}

func (n *Ngrok) awaitURL(ctx context.Context, done <-chan struct{}, timeoutSecs int) (Endpoint, error) {
	deadline := time.After(time.Duration(timeoutSecs) * time.Second)
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()

	client := &http.Client{Timeout: 2 * time.Second}
	for {
		select {
		case <-deadline:
			return Endpoint{}, fmt.Errorf("ngrok did not report a tunnel URL within %ds", timeoutSecs)
		case <-done:
			return Endpoint{}, fmt.Errorf("ngrok exited before creating the tunnel (is the authtoken valid?)")
		case <-tick.C:
			data, err := getJSON(ctx, client, n.apiURL+"/api/tunnels")
			if err != nil {
				continue // agent not ready yet
			}
			if u := parseNgrokTunnels(data); u != "" {
				return Endpoint{Provider: "ngrok", URL: u}, nil
			}
		}
	}
}

// Stop kills the ngrok agent.
func (n *Ngrok) Stop() error {
	n.mu.Lock()
	cmd := n.cmd
	done := n.done
	n.mu.Unlock()
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

func getJSON(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
