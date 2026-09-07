package xdg

import (
	"path/filepath"
	"testing"
)

func TestFallbackWhenEnvUnset(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	for _, e := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(e, "")
	}
	want := filepath.Join("/home/u", ".config", "miodesk")
	got, err := ConfigDir()
	if err != nil {
		t.Fatalf("ConfigDir: %v", err)
	}
	if got != want {
		t.Errorf("ConfigDir = %q, want %q", got, want)
	}
	if got, _ := DataDir(); got != filepath.Join("/home/u", ".local/share", "miodesk") {
		t.Errorf("DataDir = %q", got)
	}
	if got, _ := StateDir(); got != filepath.Join("/home/u", ".local/state", "miodesk") {
		t.Errorf("StateDir = %q", got)
	}
	if got, _ := CacheDir(); got != filepath.Join("/home/u", ".cache", "miodesk") {
		t.Errorf("CacheDir = %q", got)
	}
}

func TestEnvOverride(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/custom/cfg")
	t.Setenv("XDG_STATE_HOME", "/custom/state")
	got, err := ConfigDir()
	if err != nil {
		t.Fatalf("ConfigDir: %v", err)
	}
	if got != filepath.Join("/custom/cfg", "miodesk") {
		t.Errorf("ConfigDir = %q", got)
	}
	if got, _ := StateDir(); got != filepath.Join("/custom/state", "miodesk") {
		t.Errorf("StateDir = %q", got)
	}
}

func TestRelativeEnvRejected(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "relative/path")
	if _, err := ConfigDir(); err == nil {
		t.Fatal("expected error for relative XDG_CONFIG_HOME")
	}
}

func TestNoHomeErrors(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	if _, err := ConfigDir(); err == nil {
		t.Fatal("expected error when HOME and XDG_CONFIG_HOME are unset")
	}
}
