package cli

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Chillizu/miodesk/internal/config"
	"github.com/Chillizu/miodesk/internal/server"
	"github.com/Chillizu/miodesk/internal/tunnel"
	"github.com/Chillizu/miodesk/internal/workspace"
)

// runConnect starts the local MCP server and exposes it through a tunnel
// provider. Missing tunnel binaries degrade to a clear error with
// alternatives — a tunnel is never a hidden requirement.
func runConnect(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("connect", stderr)
	providerFlag := fs.String("provider", "", "connection mode: openai (default) or custom endpoint")
	urlFlag := fs.String("url", "", "public endpoint URL (required for --provider custom)")
	portFlag := fs.Int("port", 0, "local listen port (default: from config, 0 = random)")
	timeoutFlag := fs.Int("timeout", 30, "seconds to wait for the public endpoint")
	unsafeFlag := fs.Bool("unsafe-remote", false, "EXPLICIT: expose the MCP endpoint without authentication (development only)")
	if help, err := parseFlags(fs, args); help {
		return 0
	} else if err != nil {
		hintf(stderr, "run `miodesk connect -h`")
		return 2
	}
	if !requireNoPositional(fs, stderr) {
		return 2
	}
	portSet := flagWasSet(fs, "port")

	cfg, _, ok := loadConfig(stderr)
	if !ok {
		return 1
	}
	providerName := *providerFlag
	if providerName == "" {
		providerName = cfg.Tunnel.Provider
	}
	if providerName == "" {
		providerName = "openai"
	}
	if *urlFlag != "" && providerName != "custom" {
		errf(stderr, "--url is only valid with --provider custom")
		hintf(stderr, "use `miodesk connect --provider custom --url https://…`")
		return 2
	}
	if providerName == "custom" && *urlFlag == "" {
		errf(stderr, "custom provider requires --url")
		hintf(stderr, "use `miodesk connect --provider custom --url https://…`")
		return 2
	}
	if providerName == "openai" && *unsafeFlag {
		errf(stderr, "--unsafe-remote is not used with the OpenAI Secure MCP Tunnel")
		hintf(stderr, "the OpenAI tunnel supplies the remote boundary; omit --unsafe-remote")
		return 2
	}
	portOverride := portSet && *portFlag != cfg.Server.Port
	if portSet {
		cfg.Server.Port = *portFlag
	}
	if err := cfg.Validate(); err != nil {
		errf(stderr, "%v", err)
		return 1
	}
	configureLogging(cfg, stderr)
	if err := validateBindSecurity(cfg); err != nil {
		errf(stderr, "%v", err)
		hintf(stderr, "bind 127.0.0.1, or set remote.mode = \"token\" with remote.token; --unsafe-remote is for short tests only")
		return 1
	}
	ws, err := workspace.New(cfg.Workspace.Root)
	if err != nil {
		errf(stderr, "workspace: %v", err)
		hintf(stderr, "create the directory or run `miodesk init --workspace …`")
		return 1
	}
	if providerName == "openai" {
		// An explicit port override changes the tunnel-client's local target;
		// refresh the externally-owned profile in that case.
		return runOpenAIConnect(cfg, ws, stdout, stderr, portOverride)
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
		hintf(stderr, "check `miodesk tunnel doctor`, or provide your own HTTPS endpoint with `--provider custom --url https://…`")
		return 1
	}
	slog.Info("tunnel_ready", "provider", ep.Provider, "endpoint", ep.URL)

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
		slog.Warn("tunnel_stop_error", "provider", ep.Provider, "error", err.Error())
		warnf(stderr, "%v", err)
	} else {
		slog.Info("tunnel_stopped", "provider", ep.Provider)
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
	if unsafe {
		return "unsafe", nil
	}
	switch cfg.Remote.Mode {
	case "unsafe":
		return "unsafe", nil
	case "local":
		return "", fmt.Errorf("remote.mode is %q: miodesk refuses to open an unauthenticated remote entrance", cfg.Remote.Mode)
	case "token":
		return "token", nil
	default: // empty mode: adopt a legacy token or generate a new one.
		changed := false
		if cfg.Remote.Token == "" {
			cfg.Remote.Token = generateToken()
			changed = true
		}
		if cfg.Remote.Mode != "token" {
			cfg.Remote.Mode = "token"
			changed = true
		}
		if changed {
			path, err := config.Path()
			if err != nil {
				return "", fmt.Errorf("resolve config path for remote token: %w", err)
			}
			if err := cfg.Save(path); err != nil {
				return "", fmt.Errorf("persist remote token mode: %w", err)
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
	fs := newFlagSet("tunnel", stderr)
	if help, err := parseFlags(fs, args); help {
		return 0
	} else if err != nil {
		hintf(stderr, "run `miodesk tunnel -h`")
		return 2
	}
	if fs.NArg() == 0 {
		errf(stderr, "missing subcommand")
		hintf(stderr, "use: miodesk tunnel <list|doctor>")
		return 2
	}
	if fs.NArg() > 1 {
		errf(stderr, "unexpected positional arguments after tunnel subcommand")
		hintf(stderr, "use: miodesk tunnel <list|doctor>")
		return 2
	}
	switch fs.Arg(0) {
	case "list":
		okf(stdout, "default connection: OpenAI Secure MCP Tunnel")
		infof(stdout, "the local MCP server stays on 127.0.0.1; tunnel-client opens outbound HTTPS to OpenAI")
		infof(stdout, "custom endpoint: use `miodesk connect --provider custom --url https://…`")
		return 0
	case "doctor":
		return tunnelDoctor(stdout, stderr)
	default:
		errf(stderr, "unknown tunnel subcommand %q", fs.Arg(0))
		hintf(stderr, "use: miodesk tunnel <list|doctor>")
		return 2
	}
}

func tunnelDoctor(stdout, stderr io.Writer) int {
	cfg, _, ok := loadConfig(stderr)
	if !ok {
		return 1
	}
	if cfg.Tunnel.Provider == "openai" {
		if cfg.Tunnel.OpenAI.TunnelID == "" {
			warnf(stdout, "OpenAI Secure MCP Tunnel is not configured")
			hintf(stdout, "run `miodesk setup --tunnel-id tunnel_… --runtime-key-file <file>`")
			return 0
		}
		settings, err := openAISettingsFromConfig(cfg, true)
		if err != nil {
			warnf(stdout, "%v", err)
			return 0
		}
		if _, err := os.Stat(openAIProfilePath(settings)); err != nil {
			warnf(stdout, "tunnel-client profile is missing: %s", openAIProfilePath(settings))
			hintf(stdout, "run `miodesk setup --tunnel-id %s --runtime-key-file %s`", settings.TunnelID, settings.KeyFile)
			return 0
		}
		okf(stdout, "OpenAI tunnel-client profile: %s", openAIProfilePath(settings))
		infof(stdout, "tunnel id: %s", settings.TunnelID)
		infof(stdout, "local MCP target: %s", openAILocalMCPURL(cfg.Server.Port))
		infof(stdout, "run `tunnel-client doctor --profile %s --explain` for control-plane readiness", settings.Profile)
		return 0
	}
	if cfg.Tunnel.Provider == "custom" {
		infof(stdout, "custom endpoint mode: miodesk does not create or list a public endpoint")
	} else if cfg.Tunnel.Provider == "local" {
		infof(stdout, "local mode: no remote endpoint")
	} else {
		infof(stdout, "legacy connection mode preserved: %s", cfg.Tunnel.Provider)
	}
	infof(stdout, "remote clients require an HTTPS endpoint; local MCP clients need none")
	return 0
}
