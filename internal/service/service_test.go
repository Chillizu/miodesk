package service

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUnitPathFollowsXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/custom/cfg")
	path, err := UnitPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/custom/cfg", "systemd", "user", "miodesk.service")
	if path != want {
		t.Errorf("UnitPath = %q, want %q", path, want)
	}
}

func TestTunnelUnitPathFollowsXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/custom/cfg")
	path, err := TunnelUnitPath()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join("/custom/cfg", "systemd", "user", "miodesk-tunnel.service")
	if path != want {
		t.Errorf("TunnelUnitPath = %q, want %q", path, want)
	}
}

func TestTunnelUnitContent(t *testing.T) {
	content := TunnelUnitContent("/home/me/.local/bin/tunnel-client", "/home/me/.config/tunnel-client", "miodesk")
	for _, want := range []string{
		"Description=miodesk OpenAI Secure MCP Tunnel",
		"Requires=miodesk.service",
		"After=miodesk.service network-online.target",
		"ExecStart=/home/me/.local/bin/tunnel-client run --profile-dir /home/me/.config/tunnel-client --profile miodesk",
		"Restart=always",
		"UMask=0077",
		"NoNewPrivileges=true",
		"PrivateTmp=true",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("tunnel unit missing %q:\n%s", want, content)
		}
	}
	quoted := TunnelUnitContent("/opt/Tunnel Client/tunnel-client", "/home/me/Config Dir", "mio profile")
	if !strings.Contains(quoted, `ExecStart="/opt/Tunnel Client/tunnel-client" run --profile-dir "/home/me/Config Dir" --profile "mio profile"`) {
		t.Errorf("tunnel unit must quote arguments with spaces:\n%s", quoted)
	}
}

func TestUnitContent(t *testing.T) {
	content := UnitContent("/opt/miodesk")
	for _, want := range []string{
		"[Unit]",
		"Description=miodesk local MCP server",
		"ExecStart=/opt/miodesk serve",
		"Restart=on-failure",
		"WantedBy=default.target",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("unit missing %q:\n%s", want, content)
		}
	}
	quoted := UnitContent("/opt/Mio Desk/miodesk")
	if !strings.Contains(quoted, `ExecStart="/opt/Mio Desk/miodesk" serve`) {
		t.Errorf("unit must quote paths with spaces:\n%s", quoted)
	}
}

func TestWriteUnit(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := UnitPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeUnit("/usr/local/bin/miodesk", path); err != nil {
		t.Fatalf("writeUnit: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "ExecStart=/usr/local/bin/miodesk serve") {
		t.Errorf("written unit:\n%s", data)
	}
	if fi, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm() != 0o600 {
		t.Errorf("unit mode = %o, want 600", fi.Mode().Perm())
	}
	if fi, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm() != 0o700 {
		t.Errorf("unit directory mode = %o, want 700", fi.Mode().Perm())
	}
}

func TestSupported(t *testing.T) {
	// This test file only runs on linux in CI for this project; on other
	// platforms the service commands report a clear error instead.
	if runtime.GOOS == "linux" && !Supported() {
		t.Error("Supported should be true on linux")
	}
}
