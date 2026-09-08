package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Chillizu/miodesk/internal/buildinfo"
	"github.com/Chillizu/miodesk/internal/config"
	"github.com/Chillizu/miodesk/internal/doctor"
	"github.com/Chillizu/miodesk/internal/logging"
	"github.com/Chillizu/miodesk/internal/server"
	"github.com/Chillizu/miodesk/internal/service"
	"github.com/Chillizu/miodesk/internal/update"
	"github.com/Chillizu/miodesk/internal/workspace"
	"github.com/Chillizu/miodesk/internal/xdg"
)

// isLoopback reports whether a bind host keeps the server local.
func isLoopback(host string) bool {
	switch host {
	case "", "127.0.0.1", "::1", "localhost":
		return true
	}
	return false
}

func validateBindSecurity(cfg *config.Config) error {
	if isLoopback(cfg.Server.Host) || cfg.Remote.Mode == "token" || cfg.Remote.Mode == "unsafe" {
		return nil
	}
	return fmt.Errorf("refusing to serve on %q without authentication", cfg.Server.Host)
}

func runVersion(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("version", stderr)
	if help, err := parseFlags(fs, args); help {
		return 0
	} else if err != nil {
		hintf(stderr, "run `miodesk version -h`")
		return 2
	}
	if !requireNoPositional(fs, stderr) {
		return 2
	}
	fmt.Fprintf(stdout, "miodesk %s\ncommit: %s\nbuilt: %s\nplatform: %s\n",
		buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate, buildinfo.Platform())
	return 0
}

func runInit(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("init", stderr)
	wsFlag := fs.String("workspace", "", "workspace root to record in the config")
	force := fs.Bool("force", false, "with --workspace, update an existing config")
	if help, err := parseFlags(fs, args); help {
		return 0
	} else if err != nil {
		hintf(stderr, "run `miodesk init -h`")
		return 2
	}
	if !requireNoPositional(fs, stderr) {
		return 2
	}

	path, err := config.Path()
	if err != nil {
		errf(stderr, "%v", err)
		return 1
	}
	cfg, existed, err := config.LoadOrDefault(path)
	if err != nil {
		errf(stderr, "%v", err)
		hintf(stderr, "fix %s, or delete it and run `miodesk init`", path)
		return 1
	}

	created := !existed
	updated := false
	switch {
	case created:
		if *wsFlag != "" {
			cfg.Workspace.Root = *wsFlag
		}
	case *wsFlag != "" && cfg.Workspace.Root != *wsFlag:
		if !*force {
			warnf(stdout, "configuration already exists: %s", path)
			hintf(stdout, "re-run with --force to change the workspace root")
			return 1
		}
		cfg.Workspace.Root = *wsFlag
		updated = true
	}

	if err := cfg.Validate(); err != nil {
		errf(stderr, "%v", err)
		hintf(stderr, "fix the values in %s", path)
		return 1
	}
	if created || updated {
		if err := cfg.Save(path); err != nil {
			errf(stderr, "write config: %v", err)
			return 1
		}
		if created {
			okf(stdout, "configuration created: %s", path)
		} else {
			okf(stdout, "configuration updated: %s", path)
		}
	} else {
		okf(stdout, "configuration already exists: %s", path)
	}
	if _, err := workspace.New(cfg.Workspace.Root); err != nil {
		warnf(stdout, "workspace: %v", err)
		hintf(stdout, "create the directory before running `miodesk serve`")
	} else {
		okf(stdout, "workspace: %s", cfg.Workspace.Root)
	}
	infof(stdout, "local MCP port %d (use --port 0 only for an ephemeral local test)", cfg.Server.Port)
	if cfg.Tunnel.Provider == "openai" {
		infof(stdout, "connection: OpenAI Secure MCP Tunnel")
	} else {
		infof(stdout, "connection mode preserved: %s", cfg.Tunnel.Provider)
	}
	okf(stdout, "platform: %s", buildinfo.Platform())

	fmt.Fprintln(stdout, "\nNext:\n\n  miodesk serve\n  miodesk doctor")
	return 0
}

func runServe(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("serve", stderr)
	portFlag := fs.Int("port", 0, "listen port (default: from config, 0 = random)")
	hostFlag := fs.String("host", "", "listen host (default: from config)")
	wsFlag := fs.String("workspace", "", "workspace root override")
	stdioFlag := fs.Bool("stdio", false, "serve MCP over stdin/stdout for local clients")
	if help, err := parseFlags(fs, args); help {
		return 0
	} else if err != nil {
		hintf(stderr, "run `miodesk serve -h`")
		return 2
	}
	if !requireNoPositional(fs, stderr) {
		return 2
	}
	portSet := flagWasSet(fs, "port")
	if *stdioFlag && (portSet || *hostFlag != "") {
		errf(stderr, "--stdio cannot be combined with --port or --host")
		hintf(stderr, "use `miodesk serve --stdio --workspace …` for a local stdio server")
		return 2
	}

	cfg, cfgPath, ok := loadConfig(stderr)
	if !ok {
		return 1
	}
	if portSet {
		cfg.Server.Port = *portFlag
	}
	if *hostFlag != "" {
		cfg.Server.Host = *hostFlag
	}
	if *wsFlag != "" {
		cfg.Workspace.Root = *wsFlag
	}
	if err := cfg.Validate(); err != nil {
		errf(stderr, "%v", err)
		return 1
	}
	configureLogging(cfg, stderr)
	// Stdio has no network listener, so a configured non-loopback host is
	// irrelevant to this transport. Keep the network bind guard for HTTP only.
	if !*stdioFlag {
		if err := validateBindSecurity(cfg); err != nil {
			errf(stderr, "%v", err)
			hintf(stderr, "bind 127.0.0.1, or set remote.mode = \"token\" with remote.token in the config")
			return 1
		}
	}
	ws, err := workspace.New(cfg.Workspace.Root)
	if err != nil {
		errf(stderr, "workspace: %v", err)
		hintf(stderr, "create the directory or run `miodesk init --workspace …`")
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	s := server.New(cfg, ws)

	if *stdioFlag {
		// Protocol traffic owns stdout; the [INFO] line goes to stderr.
		infof(stderr, "miodesk MCP server ready on stdio (workspace: %s)", ws.Root())
		if err := s.RunStdio(ctx); err != nil {
			errf(stderr, "%v", err)
			return 1
		}
		return 0
	}

	okf(stdout, "config: %s", cfgPath)
	okf(stdout, "workspace: %s", ws.Root())
	ln, err := s.Listen()
	if err != nil {
		errf(stderr, "%v", err)
		var portErr *server.PortInUseError
		if errors.As(err, &portErr) {
			hintf(stderr, "use `miodesk serve --port 0` to pick a free port, or stop the process using it")
		}
		return 1
	}
	okf(stdout, "MCP server: %s", s.MCPURL())
	infof(stdout, "widget: %s", s.URL())
	switch s.AccessMode() {
	case server.AccessToken:
		okf(stdout, "access: token authentication")
	case server.AccessUnsafe:
		warnf(stdout, "access: UNSAFE — no authentication on %s", cfg.Server.Host)
	default:
		okf(stdout, "access: local (loopback only)")
	}
	infof(stdout, "press Ctrl-C to stop")

	if err := s.Serve(ctx, ln); err != nil {
		errf(stderr, "%v", err)
		return 1
	}
	okf(stdout, "server stopped")
	return 0
}

func runDoctor(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("doctor", stderr)
	jsonFlag := fs.Bool("json", false, "print machine-readable JSON")
	if help, err := parseFlags(fs, args); help {
		return 0
	} else if err != nil {
		hintf(stderr, "run `miodesk doctor -h`")
		return 2
	}
	if !requireNoPositional(fs, stderr) {
		return 2
	}

	path, err := config.Path()
	if err != nil {
		errf(stderr, "%v", err)
		return 1
	}
	cfg, _, cfgErr := config.LoadOrDefault(path)
	if cfgErr == nil {
		configureLogging(cfg, stderr)
	}
	rep := doctor.Run(path, cfg, cfgErr)

	if *jsonFlag {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			errf(stderr, "%v", err)
			return 1
		}
	} else {
		printReport(stdout, rep)
	}
	if rep.HasErrors() {
		return 1
	}
	return 0
}

func printReport(w io.Writer, rep doctor.Report) {
	for _, c := range rep.Checks {
		switch c.Status {
		case doctor.StatusOK:
			fmt.Fprint(w, "[OK] ")
		case doctor.StatusWarn:
			fmt.Fprint(w, "[WARN] ")
		case doctor.StatusInfo:
			fmt.Fprint(w, "[INFO] ")
		default:
			fmt.Fprint(w, "[ERROR] ")
		}
		if c.Detail != "" {
			fmt.Fprintf(w, "%s: %s\n", c.Name, c.Detail)
		} else {
			fmt.Fprintln(w, c.Name)
		}
		if c.Hint != "" {
			fmt.Fprintf(w, "     Hint: %s\n", c.Hint)
		}
	}
}

type serverState struct {
	PID       int    `json:"pid"`
	Port      int    `json:"port"`
	URL       string `json:"url"`
	StartedAt string `json:"started_at"`
}

func statusConnection(stderr io.Writer) (remote, tunnelProvider, tunnelService string) {
	remote = "local"
	tunnelProvider = "local"
	tunnelService = "not-installed"
	if cfg, _, ok := loadConfig(stderr); ok {
		if cfg.Remote.Mode != "" {
			remote = cfg.Remote.Mode
		}
		if cfg.Tunnel.Provider != "" {
			tunnelProvider = cfg.Tunnel.Provider
		}
	}
	if service.Supported() && service.TunnelInstalled() {
		tunnelService = "stopped"
		if service.TunnelRunningQuick() {
			tunnelService = "running"
		}
	}
	return remote, tunnelProvider, tunnelService
}

func runStatus(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("status", stderr)
	jsonFlag := fs.Bool("json", false, "print machine-readable JSON")
	if help, err := parseFlags(fs, args); help {
		return 0
	} else if err != nil {
		hintf(stderr, "run `miodesk status -h`")
		return 2
	}
	if !requireNoPositional(fs, stderr) {
		return 2
	}
	remote, tunnelProvider, tunnelService := statusConnection(stderr)
	dir, err := xdg.StateDir()
	if err != nil {
		errf(stderr, "%v", err)
		return 1
	}
	path := filepath.Join(dir, "server.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if *jsonFlag {
			_ = json.NewEncoder(stdout).Encode(map[string]any{
				"running": false, "remote": remote, "tunnel": tunnelProvider, "tunnel_service": tunnelService,
			})
			return 0
		}
		infof(stdout, "no running server found")
		infof(stdout, "tunnel: %s (service %s)", tunnelProvider, tunnelService)
		infof(stdout, "start one: miodesk serve")
		return 0
	}
	if err != nil {
		errf(stderr, "read %s: %v", path, err)
		return 1
	}
	var st serverState
	if err := json.Unmarshal(data, &st); err != nil {
		removeStateIfMatches(path, data)
		if *jsonFlag {
			_ = json.NewEncoder(stdout).Encode(map[string]any{"running": false})
			return 0
		}
		warnf(stdout, "unreadable server state at %s", path)
		hintf(stdout, "start a fresh server: miodesk serve")
		return 0
	}

	// The state file's PID can be reused by an unrelated process (and is not
	// probeable on Windows). The local health endpoint is the authoritative
	// liveness check for the recorded server.
	alive := st.PID > 0 && serverHealth(st.URL)
	// Keep a structurally valid state file when a single health probe fails.
	// The server removes it on a normal shutdown and overwrites it on startup;
	// retaining it here lets status recover from a transient timeout instead of
	// forgetting a live server until its next restart.
	if *jsonFlag {
		_ = json.NewEncoder(stdout).Encode(map[string]any{
			"running":        alive,
			"pid":            st.PID,
			"url":            st.URL,
			"port":           st.Port,
			"started_at":     st.StartedAt,
			"remote":         remote,
			"tunnel":         tunnelProvider,
			"tunnel_service": tunnelService,
		})
		return 0
	}
	if alive {
		okf(stdout, "server running (pid %d): %s", st.PID, st.URL)
		infof(stdout, "remote access: %s", remote)
		infof(stdout, "tunnel: %s (service %s)", tunnelProvider, tunnelService)
	} else {
		warnf(stdout, "stale or unreachable state: server at %s is not responding", st.URL)
		hintf(stdout, "start a fresh server: miodesk serve")
	}
	return 0
}

func removeStateIfMatches(path string, expected []byte) {
	current, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(current, expected) {
		return
	}
	_ = os.Remove(path)
}

func serverHealth(baseURL string) bool {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !isLoopback(u.Hostname()) {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(u.String(), "/")+"/healthz", nil)
	if err != nil {
		return false
	}
	client := &http.Client{
		Timeout: time.Second,
		Transport: &http.Transport{
			Proxy: nil,
		},
		// The state file is local metadata; a health probe must never follow a
		// redirect to another host or leak into a proxy-visible request.
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func runService(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("service", stderr)
	if help, err := parseFlags(fs, args); help {
		return 0
	} else if err != nil {
		hintf(stderr, "run `miodesk service -h`")
		return 2
	}
	if fs.NArg() == 0 {
		errf(stderr, "missing verb")
		hintf(stderr, "use: miodesk service <install|start|stop|restart|status|uninstall>")
		return 2
	}
	if fs.NArg() > 1 {
		errf(stderr, "unexpected positional arguments after service verb")
		hintf(stderr, "use: miodesk service <install|start|stop|restart|status|uninstall>")
		return 2
	}
	if !service.Supported() {
		errf(stderr, "the systemd user service is only available on Linux (this is %s)", buildinfo.Platform())
		return 1
	}
	verb := fs.Arg(0)
	switch verb {
	case "install":
		cfg, _, ok := loadConfig(stderr)
		if !ok {
			return 1
		}
		var tunnelSettings *openAISettings
		if cfg.Tunnel.Provider == "openai" && cfg.Tunnel.OpenAI.TunnelID != "" {
			settings, err := openAISettingsFromConfig(cfg, true)
			if err != nil {
				errf(stderr, "%v", err)
				hintf(stderr, "run `miodesk setup` to repair the OpenAI tunnel configuration")
				return 1
			}
			if _, err := os.Stat(openAIProfilePath(settings)); err != nil {
				errf(stderr, "OpenAI tunnel-client profile is missing: %s", openAIProfilePath(settings))
				hintf(stderr, "run `miodesk setup` to regenerate the tunnel-client profile")
				return 1
			}
			tunnelSettings = &settings
		}
		if err := service.Install(); err != nil {
			errf(stderr, "%v", err)
			return 1
		}
		if tunnelSettings != nil {
			if err := service.InstallTunnel(tunnelSettings.ClientPath, tunnelSettings.ProfileDir, tunnelSettings.Profile); err != nil {
				errf(stderr, "%v", err)
				return 1
			}
			okf(stdout, "services installed: %s.service + %s.service", service.Name, service.TunnelName)
			infof(stdout, "start both now: miodesk service start")
			infof(stdout, "enable both at login when ready: systemctl --user enable %s %s", service.Name, service.TunnelName)
		} else {
			if err := service.RemoveTunnel(); err != nil {
				warnf(stderr, "remove stale tunnel service: %v", err)
			}
			okf(stdout, "service installed: %s.service", service.Name)
			infof(stdout, "start it now: miodesk service start")
			infof(stdout, "enable at login when ready: systemctl --user enable %s", service.Name)
		}
		return 0
	case "uninstall":
		if err := service.Uninstall(); err != nil {
			errf(stderr, "%v", err)
			return 1
		}
		okf(stdout, "service uninstalled")
		return 0
	case "start", "stop", "restart":
		var err error
		switch verb {
		case "start":
			err = service.Start()
		case "stop":
			err = service.Stop()
		case "restart":
			err = service.Restart()
		}
		if err != nil {
			errf(stderr, "%v", err)
			return 1
		}
		okf(stdout, "service %s", verb)
		return 0
	case "status":
		st, err := service.Status()
		if st == "" {
			errf(stderr, "%v", err)
			return 1
		}
		okf(stdout, "miodesk service: %s", st)
		return 0
	default:
		errf(stderr, "unknown service verb %q", verb)
		hintf(stderr, "use install, start, stop, restart, status, or uninstall")
		return 2
	}
}

const defaultReleaseManifestURL = "https://github.com/Chillizu/miodesk/releases/latest/download/manifest.json"

var releaseManifestURL = defaultReleaseManifestURL

func runUpdate(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("update", stderr)
	from := fs.String("from", "", "release manifest URL override (JSON: {version, assets: {\"os/arch\": {url, sha256}}})")
	checkOnly := fs.Bool("check", false, "report the available version without replacing anything")
	if help, err := parseFlags(fs, args); help {
		return 0
	} else if err != nil {
		hintf(stderr, "run `miodesk update -h`")
		return 2
	}
	if !requireNoPositional(fs, stderr) {
		return 2
	}
	feedURL := *from
	if feedURL == "" {
		feedURL = releaseManifestURL
	}

	if *checkOnly {
		out, err := update.Check(buildinfo.Version, feedURL)
		if err != nil {
			errf(stderr, "%v", err)
			return 1
		}
		if out.UpToDate {
			okf(stdout, "miodesk %s is up to date (latest %s)", out.Current, out.Latest)
		} else {
			infof(stdout, "miodesk %s -> %s available (run without --check to apply)", out.Current, out.Latest)
		}
		return 0
	}

	out, err := update.Update(buildinfo.Version, feedURL)
	if err != nil {
		errf(stderr, "%v", err)
		return 1
	}
	if out.UpToDate {
		okf(stdout, "miodesk %s is up to date", out.Current)
		return 0
	}
	okf(stdout, "updated %s -> %s (%d bytes at %s)", out.Current, out.Latest, out.Bytes, out.Replaced)
	infof(stdout, "restart miodesk to use the new version")
	return 0
}

// runLogs shows miodesk's own logs. Foreground servers print to
// stdout/stderr (nothing to collect); the systemd user service logs to the
// journal, which this command reads. The configured bearer token is
// redacted from the output.
func runLogs(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("logs", stderr)
	follow := fs.Bool("follow", false, "keep streaming new log lines")
	n := fs.Int("n", 100, "number of lines to show")
	jsonFlag := fs.Bool("json", false, "print journal records as JSON")
	since := fs.String("since", "", "journal start time, e.g. 10m or today")
	until := fs.String("until", "", "journal end time")
	grep := fs.String("grep", "", "journal message pattern to filter")
	if help, err := parseFlags(fs, args); help {
		return 0
	} else if err != nil {
		hintf(stderr, "run `miodesk logs -h`")
		return 2
	}
	if !requireNoPositional(fs, stderr) {
		return 2
	}
	if *n < 0 {
		errf(stderr, "logs line count must not be negative")
		hintf(stderr, "use `miodesk logs -n 0` for no historical lines")
		return 2
	}
	if !service.Supported() || !service.Installed() {
		infof(stdout, "no collected logs: foreground servers print to stdout/stderr")
		infof(stdout, "run `miodesk service install && miodesk service start` to collect logs")
		return 0
	}

	journalArgs := []string{"--user", "-u", service.Name}
	if service.TunnelInstalled() {
		journalArgs = append(journalArgs, "-u", service.TunnelName)
	}
	journalArgs = append(journalArgs, "-n", strconv.Itoa(*n), "--no-pager")
	if *jsonFlag {
		journalArgs = append(journalArgs, "-o", "json-pretty")
	} else {
		journalArgs = append(journalArgs, "-o", "short-iso")
	}
	if *since != "" {
		journalArgs = append(journalArgs, "--since", normalizeJournalTime(*since))
	}
	if *until != "" {
		journalArgs = append(journalArgs, "--until", normalizeJournalTime(*until))
	}
	if *grep != "" {
		journalArgs = append(journalArgs, "-g", *grep)
	}
	if *follow {
		journalArgs = append(journalArgs, "--follow")
	}
	var ctx context.Context
	var stop context.CancelFunc
	if *follow {
		ctx, stop = signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	} else {
		ctx, stop = context.WithTimeout(context.Background(), 30*time.Second)
	}
	defer stop()
	cmd := exec.CommandContext(ctx, "journalctl", journalArgs...)
	stdoutRedactor := &redactingWriter{w: stdout}
	stderrRedactor := &redactingWriter{w: stderr}
	cmd.Stdout = stdoutRedactor
	cmd.Stderr = stderrRedactor

	// Defense in depth: the token never reaches the terminal, even if
	// something logged it.
	if cfg, _, ok := loadConfig(stderr); ok && cfg.Remote.Token != "" {
		secret := cfg.Remote.Token
		stdoutRedactor.secret = secret
		stderrRedactor.secret = secret
	}
	if err := cmd.Run(); err != nil {
		_ = stdoutRedactor.Flush()
		_ = stderrRedactor.Flush()
		if errors.Is(ctx.Err(), context.Canceled) {
			return 0
		}
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			errf(stderr, "journalctl timed out after 30s")
			return 1
		}
		errf(stderr, "journalctl: %v (is the systemd user session available?)", err)
		return 1
	}
	if err := stdoutRedactor.Flush(); err != nil {
		errf(stderr, "write logs: %v", err)
		return 1
	}
	if err := stderrRedactor.Flush(); err != nil {
		errf(stderr, "write log diagnostics: %v", err)
		return 1
	}
	return 0
}

// normalizeJournalTime makes the convenient CLI form "10m" work with
// journalctl, whose relative-time grammar expects "10 minutes ago".
func normalizeJournalTime(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}
	if strings.HasSuffix(value, "d") {
		if n, err := strconv.Atoi(strings.TrimSuffix(value, "d")); err == nil && n > 0 {
			return fmt.Sprintf("%d days ago", n)
		}
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return value
	}
	switch {
	case duration%time.Hour == 0:
		return fmt.Sprintf("%d hours ago", int(duration/time.Hour))
	case duration%time.Minute == 0:
		return fmt.Sprintf("%d minutes ago", int(duration/time.Minute))
	default:
		return fmt.Sprintf("%d seconds ago", int(duration/time.Second))
	}
}

func configureLogging(cfg *config.Config, stderr io.Writer) {
	settings := logging.Settings{Level: cfg.Logging.Level, Format: cfg.Logging.Format}
	if value := os.Getenv("MIODESK_LOG"); value != "" {
		settings.Level = value
	}
	if value := os.Getenv("MIODESK_LOG_FORMAT"); value != "" {
		settings.Format = value
	}
	if err := logging.Configure(settings, stderr); err != nil {
		warnf(stderr, "logging configuration ignored: %v", err)
		_ = logging.Configure(logging.Settings{Level: "info", Format: "text"}, stderr)
	}
}

type redactingWriter struct {
	w       io.Writer
	secret  string
	pending string
}

func (r *redactingWriter) Write(p []byte) (int, error) {
	if r.secret == "" {
		return r.w.Write(p)
	}
	combined := r.pending + string(p)
	safeLen := len(combined)
	// Retain only a suffix that could become the beginning of the secret in
	// the next write. Complete occurrences are safe to emit now.
	firstCandidate := len(combined) - len(r.secret) + 1
	if firstCandidate < 0 {
		firstCandidate = 0
	}
	for i := firstCandidate; i < len(combined); i++ {
		suffix := combined[i:]
		if len(suffix) < len(r.secret) && strings.HasPrefix(r.secret, suffix) {
			safeLen = i
			break
		}
	}
	emit := combined[:safeLen]
	r.pending = combined[safeLen:]
	if emit == "" {
		return len(p), nil
	}
	_, err := io.WriteString(r.w, strings.ReplaceAll(emit, r.secret, "[redacted]"))
	return len(p), err
}

func (r *redactingWriter) Flush() error {
	if r.pending == "" {
		return nil
	}
	pending := strings.ReplaceAll(r.pending, r.secret, "[redacted]")
	r.pending = ""
	_, err := io.WriteString(r.w, pending)
	return err
}

func runConfigPath(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("config", stderr)
	if help, err := parseFlags(fs, args); help {
		return 0
	} else if err != nil {
		hintf(stderr, "run `miodesk config -h`")
		return 2
	}
	if !requireNoPositional(fs, stderr) {
		return 2
	}
	path, err := config.Path()
	if err != nil {
		errf(stderr, "%v", err)
		return 1
	}
	fmt.Fprintln(stdout, path)
	return 0
}

func runWorkspace(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("workspace", stderr)
	if help, err := parseFlags(fs, args); help {
		return 0
	} else if err != nil {
		hintf(stderr, "run `miodesk workspace -h`")
		return 2
	}
	if !requireNoPositional(fs, stderr) {
		return 2
	}
	cfg, _, ok := loadConfig(stderr)
	if !ok {
		return 1
	}
	fmt.Fprintln(stdout, cfg.Workspace.Root)
	return 0
}
