package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A fake systemctl makes every lifecycle test independent of host systemd.
func TestCoreLifecycleNeverControlsTunnel(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	log := filepath.Join(dir, "systemctl.log")
	executable := filepath.Join(dir, "systemctl")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$MIO_TEST_SYSTEMCTL_LOG\"\ncase \"$*\" in *is-active*) echo active;; *is-enabled*) echo enabled;; esac\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MIO_TEST_SYSTEMCTL_LOG", log)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	// A tunnel unit is present and remains after uninstalling Core.
	tunnelPath, err := TunnelUnitPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(tunnelPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tunnelPath, []byte("independent tunnel\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// Also mark Core as installed in the test XDG location.
	corePath, err := UnitPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(corePath, []byte("Core\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Start(); err != nil {
		t.Fatal(err)
	}
	if err := Restart(); err != nil {
		t.Fatal(err)
	}
	if err := Stop(); err != nil {
		t.Fatal(err)
	}
	if st, err := Status(); err != nil || !strings.Contains(st, "active=") {
		t.Fatalf("core status %q: %v", st, err)
	}
	if err := Uninstall(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if strings.Contains(line, "miodesk-tunnel") {
			t.Errorf("Core lifecycle unexpectedly acted on tunnel: %q", line)
		}
	}
	if _, err := os.Stat(tunnelPath); err != nil {
		t.Fatalf("tunnel unit changed by Core uninstall: %v", err)
	}
	if _, err := os.Stat(corePath); !os.IsNotExist(err) {
		t.Errorf("Core unit should be removed, stat: %v", err)
	}
}

func TestTunnelLifecycleOnlyControlsTunnel(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "systemctl.log")
	exe := filepath.Join(dir, "systemctl")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$MIO_TEST_SYSTEMCTL_LOG\"\ncase \"$*\" in *is-active*) echo active;; *is-enabled*) echo enabled;; esac\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MIO_TEST_SYSTEMCTL_LOG", log)
	if err := StartTunnel(); err != nil {
		t.Fatal(err)
	}
	if err := RestartTunnel(); err != nil {
		t.Fatal(err)
	}
	if err := StopTunnel(); err != nil {
		t.Fatal(err)
	}
	if _, err := StatusTunnel(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if !strings.Contains(line, "miodesk-tunnel") {
			t.Errorf("tunnel lifecycle unexpectedly acted on Core: %q", line)
		}
	}
}
