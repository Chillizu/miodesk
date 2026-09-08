// Package service manages miodesk's systemd user services on Linux. The local
// MCP server and the optional OpenAI tunnel-client stay separate processes so
// each can be supervised and diagnosed independently.
package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	// Name is the local MCP server unit name (miodesk.service).
	Name = "miodesk"
	// TunnelName is the companion OpenAI tunnel unit name
	// (miodesk-tunnel.service).
	TunnelName = "miodesk-tunnel"

	systemctlTimeout = 30 * time.Second
	probeTimeout     = 5 * time.Second
)

// UnitPath returns the local server user-unit file location.
func UnitPath() (string, error) { return unitPath(Name) }

// TunnelUnitPath returns the OpenAI tunnel companion user-unit file location.
func TunnelUnitPath() (string, error) { return unitPath(TunnelName) }

func unitPath(name string) (string, error) {
	base, err := configHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "systemd", "user", name+".service"), nil
}

// UnitContent renders the local server unit. The binary path is baked in at
// install time; miodesk itself owns config, port, logging, and server lifecycle.
func UnitContent(binPath string) string {
	return fmt.Sprintf(`[Unit]
Description=miodesk local MCP server
After=default.target

[Service]
ExecStart=%s serve
SyslogIdentifier=miodesk
Restart=on-failure
RestartSec=2

[Install]
WantedBy=default.target
`, systemdExecArg(binPath))
}

// TunnelUnitContent renders the companion OpenAI tunnel-client unit. The
// profile remains owned by tunnel-client; miodesk only supervises the exact
// documented `run --profile` command and keeps it ordered after the local MCP
// server. No runtime key material is copied into the unit.
func TunnelUnitContent(clientPath, profileDir, profile string) string {
	return fmt.Sprintf(`[Unit]
Description=miodesk OpenAI Secure MCP Tunnel
Requires=%s.service
After=%s.service network-online.target
Wants=network-online.target

[Service]
ExecStart=%s run --profile-dir %s --profile %s
SyslogIdentifier=miodesk-tunnel
Restart=always
RestartSec=3
UMask=0077
NoNewPrivileges=true
PrivateTmp=true

[Install]
WantedBy=default.target
`, Name, Name, systemdExecArg(clientPath), systemdExecArg(profileDir), systemdExecArg(profile))
}

// systemdExecArg quotes an argument only when systemd's command-line parser
// needs it and escapes percent specifiers. ExecStart is not a shell command.
func systemdExecArg(arg string) string {
	escaped := strings.ReplaceAll(arg, "%", "%%")
	if !strings.ContainsAny(escaped, " \t\r\n\\\"") {
		return escaped
	}
	escaped = strings.ReplaceAll(escaped, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}

// Supported reports whether this platform can manage a systemd user service.
func Supported() bool { return runtime.GOOS == "linux" }

// Install writes the local server unit and reloads the user manager.
func Install() error {
	if !Supported() {
		return fmt.Errorf("service install requires Linux with systemd (this is %s)", runtime.GOOS)
	}
	bin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve miodesk binary: %w", err)
	}
	bin, err = filepath.EvalSymlinks(bin)
	if err != nil {
		return err
	}
	path, err := UnitPath()
	if err != nil {
		return err
	}
	if err := writeUnitContent(UnitContent(bin), path, ".miodesk.service-*"); err != nil {
		return err
	}
	return systemctl("daemon-reload")
}

// InstallTunnel writes the companion tunnel unit and reloads the user manager.
// clientPath should already be resolved to an executable and profileDir should
// be absolute so login-time startup does not depend on PATH or cwd.
func InstallTunnel(clientPath, profileDir, profile string) error {
	if !Supported() {
		return fmt.Errorf("tunnel service install requires Linux with systemd (this is %s)", runtime.GOOS)
	}
	if clientPath == "" || profileDir == "" || profile == "" {
		return fmt.Errorf("tunnel service needs client path, profile directory, and profile name")
	}
	path, err := TunnelUnitPath()
	if err != nil {
		return err
	}
	if err := writeUnitContent(TunnelUnitContent(clientPath, profileDir, profile), path, ".miodesk-tunnel.service-*"); err != nil {
		return err
	}
	return systemctl("daemon-reload")
}

// writeUnit is retained for focused server-unit tests.
func writeUnit(binPath, path string) error {
	return writeUnitContent(UnitContent(binPath), path, ".miodesk.service-*")
}

func writeUnitContent(content, path, pattern string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// Uninstall stops, disables, and removes every installed miodesk unit.
func Uninstall() error {
	serverInstalled := Installed()
	tunnelInstalled := TunnelInstalled()
	if !serverInstalled && !tunnelInstalled {
		path, _ := UnitPath()
		return fmt.Errorf("service is not installed (%s)", path)
	}
	if tunnelInstalled {
		if err := removeUnit(TunnelName); err != nil {
			return err
		}
	}
	if serverInstalled {
		if err := removeUnit(Name); err != nil {
			return err
		}
	}
	return systemctl("daemon-reload")
}

// RemoveTunnel removes only the companion unit when present. This is useful
// when a user switches away from the OpenAI provider and reinstalls services.
func RemoveTunnel() error {
	if !Supported() || !TunnelInstalled() {
		return nil
	}
	if err := removeUnit(TunnelName); err != nil {
		return err
	}
	return systemctl("daemon-reload")
}

func removeUnit(name string) error {
	path, err := unitPath(name)
	if err != nil {
		return err
	}
	quietSystemctl("stop", name)
	quietSystemctl("disable", name)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

// Start starts the server and, when installed, its tunnel companion.
func Start() error {
	if err := systemctl("start", Name); err != nil {
		return err
	}
	if TunnelInstalled() {
		return systemctl("start", TunnelName)
	}
	return nil
}

// Stop stops the tunnel first so no remote requests arrive while the local
// server is shutting down.
func Stop() error {
	if TunnelInstalled() {
		quietSystemctl("stop", TunnelName)
	}
	return systemctl("stop", Name)
}

// Restart restarts the local server, then refreshes the tunnel companion.
func Restart() error {
	if err := systemctl("restart", Name); err != nil {
		return err
	}
	if TunnelInstalled() {
		return systemctl("restart", TunnelName)
	}
	return nil
}

// Status summarizes both installed units via systemctl.
func Status() (string, error) {
	serverStatus, err := unitStatus(Name)
	if err != nil {
		return "", err
	}
	if !TunnelInstalled() {
		return "server " + serverStatus + "; tunnel not-installed", nil
	}
	tunnelStatus, err := unitStatus(TunnelName)
	if err != nil {
		return "", err
	}
	return "server " + serverStatus + "; tunnel " + tunnelStatus, nil
}

func unitStatus(name string) (string, error) {
	active, _ := output("is-active", name)
	if active == "" {
		return "", fmt.Errorf("systemctl --user is-active %s returned nothing (is the systemd user session available?)", name)
	}
	enabled, _ := output("is-enabled", name)
	if enabled == "" {
		enabled = "unknown"
	}
	return fmt.Sprintf("active=%s enabled=%s", active, enabled), nil
}

func systemctl(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), systemctlTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "systemctl", append([]string{"--user"}, args...)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("systemctl --user %s timed out after %s", strings.Join(args, " "), systemctlTimeout)
		}
		return fmt.Errorf("systemctl --user %s: %w (is systemd user session available?)",
			strings.Join(args, " "), err)
	}
	return nil
}

func quietSystemctl(args ...string) {
	ctx, cancel := context.WithTimeout(context.Background(), systemctlTimeout)
	defer cancel()
	_ = exec.CommandContext(ctx, "systemctl", append([]string{"--user"}, args...)...).Run()
}

func output(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", append([]string{"--user"}, args...)...).Output()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return strings.TrimSpace(string(out)), err
}

// Installed reports whether the local server unit exists.
func Installed() bool { return unitInstalled(Name) }

// TunnelInstalled reports whether the companion tunnel unit exists.
func TunnelInstalled() bool { return unitInstalled(TunnelName) }

func unitInstalled(name string) bool {
	path, err := unitPath(name)
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// RunningQuick performs a bounded is-active probe for the local server.
func RunningQuick() bool { return runningQuick(Name) }

// TunnelRunningQuick performs a bounded is-active probe for the tunnel client.
func TunnelRunningQuick() bool { return runningQuick(TunnelName) }

func runningQuick(name string) bool {
	if !Supported() {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "systemctl", "--user", "is-active", "--quiet", name)
	return cmd.Run() == nil && ctx.Err() == nil
}

func configHome() (string, error) {
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		if !filepath.IsAbs(v) {
			return "", fmt.Errorf("$XDG_CONFIG_HOME must be an absolute path, got %q", v)
		}
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".config"), nil
}
