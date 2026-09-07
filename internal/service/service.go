// Package service manages the miodesk systemd user service on Linux. The
// unit is minimal: miodesk itself owns config, port, logging, and lifecycle.
package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Name is the unit name (miodesk.service).
const Name = "miodesk"

// UnitPath returns the user-unit file location:
// $XDG_CONFIG_HOME/systemd/user/miodesk.service (fallback ~/.config/...).
func UnitPath() (string, error) {
	base, err := configHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "systemd", "user", Name+".service"), nil
}

// UnitContent renders the unit file. The binary path is baked in at install
// time; keep the unit boring and let miodesk handle everything else.
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

// systemdExecArg quotes a path only when systemd's command-line parser needs
// it and escapes percent specifiers. ExecStart is not a shell command, but an
// unquoted path containing spaces would otherwise be split into arguments.
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
func Supported() bool {
	return runtime.GOOS == "linux"
}

// Install writes the unit and reloads the user manager.
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
	if err := writeUnit(bin, path); err != nil {
		return err
	}
	return systemctl("daemon-reload")
}

func writeUnit(binPath, path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".miodesk.service-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(UnitContent(binPath)); err != nil {
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

// Uninstall stops, disables, and removes the unit if present. stop/disable
// failures are best-effort and stay silent — the goal is a clean removal.
func Uninstall() error {
	path, err := UnitPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("service is not installed (%s)", path)
	}
	quietSystemctl("stop", Name)
	quietSystemctl("disable", Name)
	if err := os.Remove(path); err != nil {
		return err
	}
	return systemctl("daemon-reload")
}

// Start, Stop, Restart map to their systemctl verbs.
func Start() error   { return systemctl("start", Name) }
func Stop() error    { return systemctl("stop", Name) }
func Restart() error { return systemctl("restart", Name) }

// Status summarizes the unit state via systemctl.
func Status() (string, error) {
	active, _ := output("is-active", Name)
	if active == "" {
		return "", fmt.Errorf("systemctl --user is-active returned nothing (is the systemd user session available?)")
	}
	enabled, _ := output("is-enabled", Name)
	if enabled == "" {
		enabled = "unknown"
	}
	return fmt.Sprintf("active=%s enabled=%s", active, enabled), nil
}

func systemctl(args ...string) error {
	cmd := exec.Command("systemctl", append([]string{"--user"}, args...)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("systemctl --user %s: %w (is systemd user session available?)",
			strings.Join(args, " "), err)
	}
	return nil
}

func quietSystemctl(args ...string) {
	_ = exec.Command("systemctl", append([]string{"--user"}, args...)...).Run()
}

func output(args ...string) (string, error) {
	out, err := exec.Command("systemctl", append([]string{"--user"}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}

// installed reports whether the unit file exists (used by doctor).
func installed() bool {
	path, err := UnitPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// Installed is the doctor-facing check.
func Installed() bool { return installed() }

// RunningQuick performs a bounded is-active probe; errors count as not
// running so doctor never hangs on a broken systemd session.
func RunningQuick() bool {
	if !Supported() {
		return false
	}
	done := make(chan bool, 1)
	go func() {
		cmd := exec.Command("systemctl", "--user", "is-active", "--quiet", Name)
		done <- cmd.Run() == nil
	}()
	select {
	case r := <-done:
		return r
	case <-time.After(2 * time.Second):
		return false
	}
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
