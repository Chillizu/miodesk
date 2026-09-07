package cli

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"miodesk/internal/config"
	"miodesk/internal/server"
	"miodesk/internal/tunnel"
	"miodesk/internal/workspace"
)

// runConnect starts the local MCP server and exposes it through a tunnel
// provider. Missing tunnel binaries degrade to a clear error with
// alternatives — a tunnel is never a hidden requirement.
func runConnect(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("connect", stderr)
	providerFlag := fs.String("provider", "", "tunnel provider: auto, local, cloudflare, ngrok, tailscale, custom (default: from config)")
	urlFlag := fs.String("url", "", "public endpoint URL (required for --provider custom)")
	portFlag := fs.Int("port", -1, "local listen port (default: from config, 0 = random)")
	timeoutFlag := fs.Int("timeout", 30, "seconds to wait for the public endpoint")
	unsafeFlag := fs.Bool("unsafe-remote", false, "EXPLICIT: expose the MCP endpoint without authentication (development only)")
	if err := fs.Parse(args); err != nil {
		hintf(stderr, "run `miodesk connect -h`")
		return 2
	}

	cfg, _, ok := loadConfig(stderr)
	if !ok {
		return 1
	}
	providerName := *providerFlag
	if providerName == "" {
		providerName = cfg.Tunnel.Provider
	}
	if providerName == "" {
		providerName = "auto"
	}
	if *portFlag >= 0 {
		cfg.Server.Port = *portFlag
	}
	if err := cfg.Validate(); err != nil {
		errf(stderr, "%v", err)
		return 1
	}
	ws, err := workspace.New(cfg.Workspace.Root)
	if err != nil {
		errf(stderr, "workspace: %v", err)
		hintf(stderr, "create the directory or run `miodesk init --workspace …`")
		return 1
	}

	// A tunnel is by definition a remote entrance: it must never open
	// unauthenticated unless the operator explicitly opted in.
	if providerName != "local" {
		mode, err := resolveRemoteAuth(cfg, *unsafeFlag)
		if err != nil {
			errf(stderr, "%v", err)
			hintf(stderr, "let miodesk generate a token (default), or accept the risk with --unsafe-remote")
			return 1
		}
		cfg.Remote.Mode = mode
	}

	s := server.New(cfg, ws)
	ln, err := s.Listen()
	if err != nil {
		errf(stderr, "%v", err)
		var portErr *server.PortInUseError
		if errors.As(err, &portErr) {
			hintf(stderr, "use `miodesk connect --port 0` to pick a free port, or stop the process using it")
		}
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	provider, err := tunnel.Detect(providerName)
	if err != nil {
		ln.Close()
		errf(stderr, "%v", err)
		if h, ok := err.(interface{ Hint() string }); ok {
			hintf(stderr, "%s", h.Hint())
		}
		return 1
	}

	ep, err := provider.Start(ctx, tunnel.Options{
		LocalURL:       s.URL(),
		CustomURL:      *urlFlag,
		TimeoutSeconds: *timeoutFlag,
	})
	if err != nil {
		ln.Close()
		errf(stderr, "%v", err)
		hintf(stderr, "check `miodesk tunnel doctor`, or try another provider: miodesk tunnel list")
		return 1
	}

	okf(stdout, "local server: %s", s.URL())
	okf(stdout, "public endpoint (%s): %s", ep.Provider, ep.URL)
	switch s.AccessMode() {
	case server.AccessToken:
		okf(stdout, "access: token authentication")
		infof(stdout, "clients must send: Authorization: Bearer <token>")
		infof(stdout, "token (stored in the config, printed once): %s", cfg.Remote.Token)
		warnf(stdout, "ChatGPT connectors cannot use static bearer tokens; prefer a client that sends Authorization headers")
	case server.AccessUnsafe:
		warnf(stdout, "access: UNSAFE — no authentication; anyone with the URL can read and modify the workspace")
		warnf(stdout, "keep this exposure short-lived and stop it when done")
	}
	infof(stdout, "MCP endpoint: %s", ep.MCPURL())
	infof(stdout, "press Ctrl-C to stop")

	serveErr := s.Serve(ctx, ln)
	if err := provider.Stop(); err != nil {
		warnf(stderr, "%v", err)
	}
	if serveErr != nil {
		errf(stderr, "%v", serveErr)
		return 1
	}
	okf(stdout, "stopped")
	return 0
}

// resolveRemoteAuth returns the access mode for a remote entrance. A remote
// entrance is never unauthenticated without an explicit unsafe opt-in; with
// no configuration it generates a token and persists it.
func resolveRemoteAuth(cfg *config.Config, unsafe bool) (string, error) {
	mode := cfg.Remote.Mode
	if unsafe {
		return "unsafe", nil
	}
	switch mode {
	case "unsafe":
		return "unsafe", nil
	case "local":
		return "", fmt.Errorf("remote.mode is %q: miodesk refuses to open an unauthenticated remote entrance", mode)
	default: // "" or "token"
		if cfg.Remote.Token == "" {
			cfg.Remote.Token = generateToken()
			cfg.Remote.Mode = "token"
			path, perr := config.Path()
			if perr == nil {
				if serr := cfg.Save(path); serr != nil {
					return "", fmt.Errorf("persist generated token: %w", serr)
				}
			}
		}
		return "token", nil
	}
}

func generateToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand unavailable: %v", err)) // cannot happen on supported platforms
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func runTunnel(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		errf(stderr, "missing subcommand")
		hintf(stderr, "use: miodesk tunnel <list|doctor>")
		return 2
	}
	switch args[0] {
	case "list":
		for _, d := range tunnel.DetectAll() {
			if d.Available {
				okf(stdout, "%s", d.Name)
			} else {
				warnf(stdout, "%s: %s", d.Name, d.Reason)
			}
		}
		infof(stdout, "use: miodesk connect --provider <name>")
		return 0
	case "doctor":
		return tunnelDoctor(stdout, stderr)
	default:
		errf(stderr, "unknown tunnel subcommand %q", args[0])
		hintf(stderr, "use: miodesk tunnel <list|doctor>")
		return 2
	}
}

func tunnelDoctor(stdout, stderr io.Writer) int {
	code := 0
	for _, d := range tunnel.DetectAll() {
		switch {
		case d.Available && (d.Name == "local" || d.Name == "custom"):
			// Always available by design; not worth a line unless asked.
		case d.Available:
			okf(stdout, "%s: available", d.Name)
		default:
			warnf(stdout, "%s: %s", d.Name, d.Reason)
			code = 0 // unavailability is a warning, not an error
		}
	}
	cfg, _, ok := loadConfig(stderr)
	if !ok {
		return 1
	}
	infof(stdout, "configured provider: %s", cfg.Tunnel.Provider)
	infof(stdout, "remote clients such as ChatGPT require HTTPS; local MCP clients need none")
	return code
}
