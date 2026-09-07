// Package doctor checks a miodesk installation and explains what to do next:
// every check reports what it found, why it matters, and how to fix it.
package doctor

import (
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"

	"miodesk/internal/config"
	"miodesk/internal/server"
	"miodesk/internal/service"
	"miodesk/internal/tunnel"
	"miodesk/internal/workspace"
)

type Status string

const (
	StatusOK    Status = "ok"
	StatusWarn  Status = "warn"
	StatusInfo  Status = "info"
	StatusError Status = "error"
)

type Check struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail,omitempty"`
	Hint   string `json:"hint,omitempty"`
}

type Report struct {
	Checks []Check `json:"checks"`
}

func (r *Report) add(name string, status Status, detail string, hint ...string) {
	h := ""
	if len(hint) > 0 {
		h = hint[0]
	}
	r.Checks = append(r.Checks, Check{Name: name, Status: status, Detail: detail, Hint: h})
}

func (r Report) HasErrors() bool {
	for _, c := range r.Checks {
		if c.Status == StatusError {
			return true
		}
	}
	return false
}

// Run performs all checks. cfg may be nil when the config could not be read;
// cfgErr carries that failure so doctor can still explain the situation.
func Run(cfgPath string, cfg *config.Config, cfgErr error) Report {
	var r Report

	if cfgErr != nil {
		r.add("config", StatusError, cfgErr.Error(),
			"fix the file, or delete it and run `miodesk init`")
		return r
	}
	r.add("config", StatusOK, cfgPath)

	ws, err := workspace.New(cfg.Workspace.Root)
	if err != nil {
		r.add("workspace", StatusError, err.Error(),
			"create the directory or point workspace.root at an existing one")
		return r
	}
	r.add("workspace", StatusOK, ws.Root())

	if err := checkWritable(ws.Root()); err != nil {
		r.add("permissions", StatusWarn, err.Error(),
			"file tools need write access to the workspace")
	} else {
		r.add("permissions", StatusOK, "workspace is writable")
	}

	srv := server.New(cfg, ws)
	names := make([]string, 0, len(srv.Tools()))
	for _, t := range srv.Tools() {
		names = append(names, t.Name)
	}
	r.add("mcp", StatusOK, fmt.Sprintf("%d tools registered (%s)", len(names), strings.Join(names, ", ")))

	switch cfg.Server.Host {
	case "127.0.0.1", "::1", "localhost":
		// local-only: the intended default
	default:
		r.add("server", StatusInfo,
			fmt.Sprintf("host %q is reachable from other machines", cfg.Server.Host),
			"prefer 127.0.0.1 unless a tunnel or proxy fronts the server")
	}
	checkPort(&r, cfg)

	checkRemoteSecurity(&r, cfg)
	checkTunnel(&r, cfg.Tunnel.Provider)
	checkService(&r)

	r.add("platform", StatusOK, runtime.GOOS+"/"+runtime.GOARCH)
	return r
}

func checkService(r *Report) {
	if !service.Supported() {
		return
	}
	if !service.Installed() {
		r.add("service", StatusInfo, "systemd user service not installed",
			"`miodesk service install` to run the server at login")
		return
	}
	if service.RunningQuick() {
		r.add("service", StatusOK, "systemd user service is running")
	} else {
		r.add("service", StatusInfo, "service installed but not running",
			"`miodesk service start`")
	}
}

func checkPort(r *Report, cfg *config.Config) {
	if cfg.Server.Port == 0 {
		r.add("server", StatusInfo, "port 0: a random free port is chosen on each start",
			"set server.port in the config for a fixed endpoint")
		return
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(cfg.Server.Host, strconv.Itoa(cfg.Server.Port)))
	if err != nil {
		r.add("server", StatusWarn, fmt.Sprintf("port %d is not available: %v", cfg.Server.Port, err),
			"stop the process using it, or run `miodesk serve --port 0`")
		return
	}
	ln.Close()
	r.add("server", StatusOK, fmt.Sprintf("port %d is free", cfg.Server.Port))
}

// checkRemoteSecurity reports the remote-access trust level. Security
// problems are WARN, never disguised as INFO; tokens are never printed.
func checkRemoteSecurity(r *Report, cfg *config.Config) {
	switch cfg.Remote.Mode {
	case "unsafe":
		r.add("remote security", StatusWarn,
			"unsafe mode: the MCP endpoint accepts unauthenticated requests",
			"set remote.mode = \"token\" with remote.token, or remove remote.mode to stay local")
	case "token":
		r.add("remote security", StatusOK, "remote authentication enabled")
	default:
		switch cfg.Server.Host {
		case "", "127.0.0.1", "::1", "localhost":
			r.add("remote security", StatusOK, "local MCP endpoint (loopback only)")
		default:
			r.add("remote security", StatusWarn,
				fmt.Sprintf("server binds %q without authentication", cfg.Server.Host),
				"set remote.mode = \"token\" with remote.token, or bind 127.0.0.1")
		}
	}
}

func checkTunnel(r *Report, provider string) {
	var available []string
	for _, d := range tunnel.DetectAll() {
		if d.Name == "local" || d.Name == "custom" {
			continue // available by design
		}
		if d.Available {
			available = append(available, d.Name)
			r.add("tunnel", StatusInfo, d.Name+" available")
		} else {
			r.add("tunnel", StatusWarn, d.Reason,
				"see `miodesk tunnel doctor` for provider details")
		}
	}

	switch provider {
	case "custom":
		r.add("tunnel", StatusInfo,
			"custom endpoint: miodesk will not create a public URL",
			"point it at your endpoint with `miodesk connect --provider custom --url https://…`")
	case "local":
		r.add("tunnel", StatusInfo, "local mode: no public endpoint")
	default:
		if len(available) == 0 {
			r.add("tunnel", StatusWarn, "no tunnel provider available",
				"install cloudflared for a zero-config quick tunnel, or expose your own endpoint with `miodesk connect --provider custom --url https://…`")
		} else if provider != "auto" {
			r.add("tunnel", StatusInfo, "configured provider: "+provider)
		}
	}

	r.add("endpoint", StatusInfo,
		"remote clients such as ChatGPT require HTTPS; local MCP clients need none",
		"run `miodesk connect` only when a public endpoint is needed")
}

func checkWritable(root string) error {
	f, err := os.CreateTemp(root, ".miodesk-doctor-*")
	if err != nil {
		return err
	}
	name := f.Name()
	f.Close()
	return os.Remove(name)
}
