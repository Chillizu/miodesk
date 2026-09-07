// Package doctor checks a miodesk installation and explains what to do next:
// every check reports what it found, why it matters, and how to fix it.
package doctor

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/Chillizu/miodesk/internal/config"
	"github.com/Chillizu/miodesk/internal/server"
	"github.com/Chillizu/miodesk/internal/service"
	"github.com/Chillizu/miodesk/internal/workspace"
	"github.com/Chillizu/miodesk/internal/xdg"
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
	if cfg == nil {
		r.add("config", StatusError, "configuration is empty", "run `miodesk init`")
		return r
	}
	if err := cfg.Validate(); err != nil {
		r.add("config", StatusError, err.Error(), "fix the values in config.toml")
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
	checkTunnel(&r, cfg)
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
		if service.RunningQuick() {
			r.add("server", StatusOK,
				fmt.Sprintf("port %d is in use by the running miodesk service", cfg.Server.Port))
			return
		}
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

func checkTunnel(r *Report, cfg *config.Config) {
	switch cfg.Tunnel.Provider {
	case "openai":
		checkOpenAITunnel(r, cfg)
	case "custom":
		r.add("tunnel", StatusInfo,
			"custom endpoint: miodesk will not create or verify a public URL",
			"use `miodesk connect --provider custom --url https://…`")
	case "local":
		r.add("tunnel", StatusInfo, "local mode: no remote endpoint")
	default:
		// Keep old provider configurations working, but do not make their
		// machine-specific availability part of the primary doctor output.
		r.add("tunnel", StatusInfo, "legacy connection mode preserved: "+cfg.Tunnel.Provider,
			"use `miodesk setup --tunnel-id tunnel_…` to switch to OpenAI Secure MCP Tunnel")
	}

	r.add("endpoint", StatusInfo,
		"the local MCP server stays on loopback; OpenAI Secure MCP Tunnel is the default remote path",
		"run `miodesk connect` only when a remote OpenAI connection is needed")
}

func checkOpenAITunnel(r *Report, cfg *config.Config) {
	if cfg.Tunnel.OpenAI.TunnelID == "" {
		r.add("tunnel", StatusInfo, "OpenAI Secure MCP Tunnel is not configured yet",
			"run `miodesk setup --tunnel-id tunnel_… --runtime-key-file <file>`")
		return
	}
	if !strings.HasPrefix(cfg.Tunnel.OpenAI.TunnelID, "tunnel_") {
		r.add("tunnel", StatusWarn, "configured OpenAI tunnel id is malformed",
			"check tunnel.openai.tunnel_id in config.toml")
		return
	}
	if cfg.Server.Port == 0 {
		r.add("tunnel", StatusWarn, "OpenAI tunnel target uses an ephemeral port",
			"set server.port to a fixed unused port, normally 8787")
	}

	client := cfg.Tunnel.OpenAI.ClientPath
	if client == "" {
		client = "tunnel-client"
	}
	if _, err := exec.LookPath(client); err != nil {
		r.add("tunnel", StatusWarn, "tunnel-client is not available",
			"install tunnel-client, then rerun `miodesk setup`")
		return
	}

	keyFile := cfg.Tunnel.OpenAI.RuntimeKeyFile
	if keyFile == "" {
		if dir, err := xdg.ConfigDir(); err == nil {
			keyFile = filepath.Join(dir, "openai-runtime-key")
		}
	}
	if info, err := os.Stat(keyFile); err != nil {
		r.add("tunnel", StatusWarn, "OpenAI runtime key file is missing",
			"create the key file at "+keyFile+" and rerun `miodesk setup`")
		return
	} else if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		r.add("tunnel", StatusWarn, "OpenAI runtime key file is group/world accessible",
			"run `chmod 600 "+keyFile+"`")
		return
	}

	profileDir := cfg.Tunnel.OpenAI.ProfileDir
	if profileDir == "" {
		if dir, err := xdg.ConfigDir(); err == nil {
			profileDir = filepath.Join(filepath.Dir(dir), "tunnel-client")
		}
	}
	profile := cfg.Tunnel.OpenAI.Profile
	if profile == "" {
		profile = config.DefaultOpenAIProfile
	}
	profilePath := filepath.Join(profileDir, profile+".yaml")
	profileInfo, err := os.Stat(profilePath)
	if err != nil {
		r.add("tunnel", StatusWarn, "OpenAI tunnel-client profile is missing",
			"run `miodesk setup` again to generate "+profilePath)
		return
	}
	if profileInfo.IsDir() || !profileInfo.Mode().IsRegular() {
		r.add("tunnel", StatusWarn, "OpenAI tunnel-client profile is not a regular file",
			"remove or replace "+profilePath+" and rerun `miodesk setup`")
		return
	}
	if runtime.GOOS != "windows" && profileInfo.Mode().Perm()&0o077 != 0 {
		r.add("tunnel", StatusWarn, "OpenAI tunnel-client profile is group/world accessible",
			"run `chmod 600 "+profilePath+"`")
		return
	}
	r.add("tunnel", StatusOK, "OpenAI Secure MCP Tunnel is configured")
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
