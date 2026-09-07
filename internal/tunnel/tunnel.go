// Package tunnel provides miodesk's pluggable public-endpoint providers.
// Core never hard-depends on a provider: every network provider is detected
// at runtime, and a missing binary degrades to a warning with alternatives —
// never to a broken server.
//
// The TunnelProvider interface is justified by five real implementations with
// a shared lifecycle (Available → Start → Stop), not by speculative design.
package tunnel

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
)

// Options are the inputs every provider needs.
type Options struct {
	// LocalURL is the local miodesk endpoint to expose, e.g.
	// http://127.0.0.1:8791.
	LocalURL string
	// CustomURL is the user-provided public endpoint (custom provider only).
	CustomURL string
	// Timeout bounds how long Start may take to learn the public URL.
	TimeoutSeconds int
}

// Endpoint is a public URL backed by one provider.
type Endpoint struct {
	Provider string
	URL      string
}

// MCPURL returns the endpoint's MCP URL (the endpoint is the base).
func (e Endpoint) MCPURL() string { return strings.TrimSuffix(e.URL, "/") + "/mcp" }

// Provider is one public-endpoint capability with a common lifecycle.
type Provider interface {
	// Name is the --provider value ("cloudflare", "ngrok", ...).
	Name() string
	// Available reports nil when the provider can be used right now, or a
	// human-readable reason why not.
	Available() error
	// Start opens the tunnel and returns once the public endpoint is known.
	// The tunnel keeps running until Stop.
	Start(ctx context.Context, opts Options) (Endpoint, error)
	// Stop tears the tunnel down. Calling Stop without Start is a no-op.
	Stop() error
}

// lookPath is a seam so tests can inject stub binaries.
var lookPath = exec.LookPath

// Timeout normalizes the requested acquisition timeout in seconds.
func Timeout(t int) int {
	if t <= 0 {
		return 30
	}
	if t > 300 {
		return 300
	}
	return t
}

// networkProviders are the tunneling providers, in auto-detection preference
// order: cloudflared works without any account, ngrok needs an authtoken,
// tailscale needs funnel enabled for the tailnet.
var networkProviders = []string{"cloudflare", "ngrok", "tailscale"}

// Names returns every provider name miodesk knows, including local/custom.
func Names() []string {
	return []string{"local", "cloudflare", "ngrok", "tailscale", "custom"}
}

// Get returns one provider by name.
func Get(name string) (Provider, error) {
	switch name {
	case "local":
		return Local{}, nil
	case "custom":
		return Custom{}, nil
	case "cloudflare":
		return &Cloudflare{}, nil
	case "ngrok":
		return &Ngrok{}, nil
	case "tailscale":
		return &Tailscale{}, nil
	case "":
		return nil, fmt.Errorf("no provider given")
	default:
		return nil, fmt.Errorf("unknown provider %q (known: %s)", name, strings.Join(Names(), ", "))
	}
}

// Detection is the outcome of probing one provider.
type Detection struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// DetectAll probes every provider, including local and custom.
func DetectAll() []Detection {
	all := []Detection{{Name: "local", Available: true}, {Name: "custom", Available: true}}
	for _, name := range networkProviders {
		p, err := Get(name)
		if err != nil {
			continue
		}
		d := Detection{Name: name}
		if reason := p.Available(); reason != nil {
			d.Reason = reason.Error()
		} else {
			d.Available = true
		}
		all = append(all, d)
	}
	return all
}

// Detect resolves a provider choice. "auto" probes network providers in
// preference order; an explicit choice never silently falls back.
func Detect(name string) (Provider, error) {
	if name != "" && name != "auto" {
		p, err := Get(name)
		if err != nil {
			return nil, err
		}
		if reason := p.Available(); reason != nil {
			return nil, unavailableError(name, reason, nil)
		}
		return p, nil
	}

	var tried []Detection
	for _, pname := range networkProviders {
		p, err := Get(pname)
		if err != nil {
			continue
		}
		if reason := p.Available(); reason != nil {
			tried = append(tried, Detection{Name: pname, Reason: reason.Error()})
			continue
		}
		return p, nil
	}
	return nil, &AutoDetectError{Tried: tried}
}

// AutoDetectError reports that no tunnel provider is usable, with per-provider
// reasons and a way out — the tunnel must never be a hidden requirement.
type AutoDetectError struct{ Tried []Detection }

func (e *AutoDetectError) Error() string {
	var parts []string
	for _, t := range e.Tried {
		parts = append(parts, fmt.Sprintf("%s: %s", t.Name, t.Reason))
	}
	return "no public endpoint available (" + strings.Join(parts, "; ") + ")"
}

func (e *AutoDetectError) Hint() string {
	return "install cloudflared for a zero-config quick tunnel, or expose your own endpoint: miodesk connect --provider custom --url https://example.com"
}

func unavailableError(name string, reason error, tried []Detection) error {
	return &ProviderUnavailableError{Name: name, Reason: reason.Error(), Tried: tried}
}

// ProviderUnavailableError reports an explicitly chosen provider that cannot
// run, plus the alternatives.
type ProviderUnavailableError struct {
	Name   string
	Reason string
	Tried  []Detection
}

func (e *ProviderUnavailableError) Error() string {
	return fmt.Sprintf("provider %q is not available: %s", e.Name, e.Reason)
}

func (e *ProviderUnavailableError) Hint() string {
	return suggestAlternatives(e.Name)
}

// suggestAlternatives names other usable tunnel providers, or the custom
// escape hatch (which needs a URL, so it is phrased accordingly).
func suggestAlternatives(name string) string {
	var alts []string
	for _, p := range DetectAll() {
		if p.Available && p.Name != name && p.Name != "local" && p.Name != "custom" {
			alts = append(alts, p.Name)
		}
	}
	if len(alts) > 0 {
		return "install it, or use an available provider: miodesk connect --provider " + alts[0]
	}
	return "install it, or expose your own endpoint: miodesk connect --provider custom --url https://example.com"
}

// local + custom ------------------------------------------------------------

// Local is the explicit "no tunnel" choice: miodesk stays local-only.
type Local struct{}

func (Local) Name() string     { return "local" }
func (Local) Available() error { return nil }
func (Local) Stop() error      { return nil }

func (Local) Start(ctx context.Context, opts Options) (Endpoint, error) {
	return Endpoint{Provider: "local", URL: opts.LocalURL}, nil
}

// Custom means the user already exposes miodesk through their own
// infrastructure — miodesk does not create or verify the public endpoint.
type Custom struct{}

func (Custom) Name() string     { return "custom" }
func (Custom) Available() error { return nil }
func (Custom) Stop() error      { return nil }

func (Custom) Start(ctx context.Context, opts Options) (Endpoint, error) {
	if opts.CustomURL == "" {
		return Endpoint{}, fmt.Errorf("custom provider needs a URL")
	}
	u, err := url.Parse(opts.CustomURL)
	if err != nil || u.Scheme != "https" && u.Scheme != "http" || u.Host == "" {
		return Endpoint{}, fmt.Errorf("custom URL %q is not a valid http(s) endpoint", opts.CustomURL)
	}
	return Endpoint{Provider: "custom", URL: strings.TrimRight(u.String(), "/")}, nil
}

// plumbing ------------------------------------------------------------------

// localPort extracts the port from a local http://host:port URL.
func localPort(localURL string) (string, error) {
	u, err := url.Parse(localURL)
	if err != nil || u.Port() == "" {
		return "", fmt.Errorf("local URL %q has no port", localURL)
	}
	return u.Port(), nil
}

// freePort asks the OS for an unused TCP port (used for ngrok's web API).
func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

func portString(p int) string { return strconv.Itoa(p) }
