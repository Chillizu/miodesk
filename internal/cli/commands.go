package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"miodesk/internal/buildinfo"
	"miodesk/internal/config"
	"miodesk/internal/doctor"
	"miodesk/internal/server"
	"miodesk/internal/service"
	"miodesk/internal/update"
	"miodesk/internal/workspace"
	"miodesk/internal/xdg"
)

// isLoopback reports whether a bind host keeps the server local.
func isLoopback(host string) bool {
	switch host {
	case "", "127.0.0.1", "::1", "localhost":
		return true
	}
	return false
}

func runVersion(w io.Writer) int {
	fmt.Fprintf(w, "miodesk %s\ncommit: %s\nbuilt: %s\nplatform: %s\n",
		buildinfo.Version, buildinfo.Commit, buildinfo.BuildDate, buildinfo.Platform())
	return 0
}

func runInit(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("init", stderr)
	wsFlag := fs.String("workspace", "", "workspace root to record in the config")
	force := fs.Bool("force", false, "with --workspace, update an existing config")
	if err := fs.Parse(args); err != nil {
		hintf(stderr, "run `miodesk init -h`")
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

	switch {
	case !existed:
		if *wsFlag != "" {
			cfg.Workspace.Root = *wsFlag
		}
		if err := cfg.Save(path); err != nil {
			errf(stderr, "write config: %v", err)
			return 1
		}
		okf(stdout, "configuration created: %s", path)
	case *wsFlag != "" && cfg.Workspace.Root != *wsFlag:
		if !*force {
			warnf(stdout, "configuration already exists: %s", path)
			hintf(stdout, "re-run with --force to change the workspace root")
			return 1
		}
		cfg.Workspace.Root = *wsFlag
		if err := cfg.Save(path); err != nil {
			errf(stderr, "write config: %v", err)
			return 1
		}
		okf(stdout, "configuration updated: %s", path)
	default:
		okf(stdout, "configuration already exists: %s", path)
	}

	if err := cfg.Validate(); err != nil {
		errf(stderr, "%v", err)
		hintf(stderr, "fix the values in %s", path)
		return 1
	}
	if _, err := workspace.New(cfg.Workspace.Root); err != nil {
		warnf(stdout, "workspace: %v", err)
		hintf(stdout, "create the directory before running `miodesk serve`")
	} else {
		okf(stdout, "workspace: %s", cfg.Workspace.Root)
	}
	infof(stdout, "port %d (0 = random free port on each start)", cfg.Server.Port)
	okf(stdout, "platform: %s", buildinfo.Platform())

	fmt.Fprintln(stdout, "\nNext:\n\n  miodesk serve\n  miodesk doctor")
	return 0
}

func runServe(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("serve", stderr)
	portFlag := fs.Int("port", -1, "listen port (default: from config, 0 = random)")
	hostFlag := fs.String("host", "", "listen host (default: from config)")
	wsFlag := fs.String("workspace", "", "workspace root override")
	stdioFlag := fs.Bool("stdio", false, "serve MCP over stdin/stdout for local clients")
	if err := fs.Parse(args); err != nil {
		hintf(stderr, "run `miodesk serve -h`")
		return 2
	}

	cfg, cfgPath, ok := loadConfig(stderr)
	if !ok {
		return 1
	}
	if *portFlag >= 0 {
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
	if !isLoopback(cfg.Server.Host) && cfg.Remote.Mode != "token" && cfg.Remote.Mode != "unsafe" {
		errf(stderr, "refusing to serve on %q without authentication", cfg.Server.Host)
		hintf(stderr, "bind 127.0.0.1, or set remote.mode = \"token\" with remote.token in the config")
		return 1
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
	if err := fs.Parse(args); err != nil {
		hintf(stderr, "run `miodesk doctor -h`")
		return 2
	}

	path, err := config.Path()
	if err != nil {
		errf(stderr, "%v", err)
		return 1
	}
	cfg, _, cfgErr := config.LoadOrDefault(path)
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

func runStatus(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("status", stderr)
	jsonFlag := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		hintf(stderr, "run `miodesk status -h`")
		return 2
	}
	dir, err := xdg.StateDir()
	if err != nil {
		errf(stderr, "%v", err)
		return 1
	}
	path := filepath.Join(dir, "server.json")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if *jsonFlag {
			_ = json.NewEncoder(stdout).Encode(map[string]any{"running": false})
			return 0
		}
		infof(stdout, "no running server found")
		infof(stdout, "start one: miodesk serve")
		return 0
	}
	if err != nil {
		errf(stderr, "read %s: %v", path, err)
		return 1
	}
	var st serverState
	if err := json.Unmarshal(data, &st); err != nil {
		warnf(stdout, "unreadable server state at %s", path)
		_ = os.Remove(path)
		hintf(stdout, "start a fresh server: miodesk serve")
		return 0
	}

	// On Windows, Signal(0) is unsupported; liveness checking there waits
	// for the milestone-3 service work.
	alive := true
	if runtime.GOOS != "windows" {
		if p, perr := os.FindProcess(st.PID); perr != nil {
			alive = false
		} else if serr := p.Signal(syscall.Signal(0)); serr != nil {
			alive = false
		}
	}
	remote := "local"
	if cfg, _, ok := loadConfig(stderr); ok && cfg.Remote.Mode != "" {
		remote = cfg.Remote.Mode
	}
	if *jsonFlag {
		_ = json.NewEncoder(stdout).Encode(map[string]any{
			"running":    alive,
			"pid":        st.PID,
			"url":        st.URL,
			"port":       st.Port,
			"started_at": st.StartedAt,
			"remote":     remote,
		})
		return 0
	}
	if alive {
		okf(stdout, "server running (pid %d): %s", st.PID, st.URL)
		infof(stdout, "remote access: %s", remote)
	} else {
		warnf(stdout, "stale state: process %d is gone", st.PID)
		_ = os.Remove(path)
		hintf(stdout, "start a fresh server: miodesk serve")
	}
	return 0
}

func runService(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("service", stderr)
	if err := fs.Parse(args); err != nil {
		hintf(stderr, "run `miodesk service -h`")
		return 2
	}
	if !service.Supported() {
		errf(stderr, "the systemd user service is only available on Linux (this is %s)", buildinfo.Platform())
		return 1
	}
	if fs.NArg() == 0 {
		errf(stderr, "missing verb")
		hintf(stderr, "use: miodesk service <install|start|stop|restart|status|uninstall>")
		return 2
	}
	verb := fs.Arg(0)
	switch verb {
	case "install":
		if err := service.Install(); err != nil {
			errf(stderr, "%v", err)
			return 1
		}
		okf(stdout, "service installed")
		infof(stdout, "start it now: miodesk service start")
		infof(stdout, "it will also start at login")
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

func runUpdate(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("update", stderr)
	from := fs.String("from", "", "release manifest URL (JSON: {version, assets: {\"os/arch\": {url, sha256}}})")
	checkOnly := fs.Bool("check", false, "report the available version without replacing anything")
	if err := fs.Parse(args); err != nil {
		hintf(stderr, "run `miodesk update -h`")
		return 2
	}
	if *from == "" {
		errf(stderr, "no release feed given")
		hintf(stderr, "use `miodesk update --from <manifest-url>`; add --check to only report")
		return 1
	}

	if *checkOnly {
		out, err := update.Check(buildinfo.Version, *from)
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

	out, err := update.Update(buildinfo.Version, *from)
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
	if err := fs.Parse(args); err != nil {
		hintf(stderr, "run `miodesk logs -h`")
		return 2
	}
	if !service.Supported() || !service.Installed() {
		infof(stdout, "no collected logs: foreground servers print to stdout/stderr")
		infof(stdout, "run `miodesk service install && miodesk service start` to collect logs")
		return 0
	}

	journalArgs := []string{"--user", "-u", service.Name, "-n", strconv.Itoa(*n), "--no-pager"}
	if *follow {
		journalArgs = append(journalArgs, "--follow")
	}
	cmd := exec.Command("journalctl", journalArgs...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	// Defense in depth: the token never reaches the terminal, even if
	// something logged it.
	if cfg, _, ok := loadConfig(stderr); ok && cfg.Remote.Token != "" {
		secret := cfg.Remote.Token
		cmd.Stdout = redactingWriter{w: stdout, secret: secret}
		cmd.Stderr = redactingWriter{w: stderr, secret: secret}
	}
	if err := cmd.Run(); err != nil {
		errf(stderr, "journalctl: %v (is the systemd user session available?)")
		return 1
	}
	return 0
}

type redactingWriter struct {
	w      io.Writer
	secret string
}

func (r redactingWriter) Write(p []byte) (int, error) {
	if strings.Contains(string(p), r.secret) {
		p = []byte(strings.ReplaceAll(string(p), r.secret, "[redacted]"))
	}
	return r.w.Write(p)
}

func runConfigPath(stdout, stderr io.Writer) int {
	path, err := config.Path()
	if err != nil {
		errf(stderr, "%v", err)
		return 1
	}
	fmt.Fprintln(stdout, path)
	return 0
}

func runWorkspace(stdout, stderr io.Writer) int {
	cfg, _, ok := loadConfig(stderr)
	if !ok {
		return 1
	}
	fmt.Fprintln(stdout, cfg.Workspace.Root)
	return 0
}
