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
}

func TestSupported(t *testing.T) {
	// This test file only runs on linux in CI for this project; on other
	// platforms the service commands report a clear error instead.
	if runtime.GOOS == "linux" && !Supported() {
		t.Error("Supported should be true on linux")
	}
}
