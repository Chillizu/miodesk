package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Chillizu/miodesk/internal/config"
	"github.com/Chillizu/miodesk/internal/service"
)

func testSystemctlStub(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "commands.log")
	file := filepath.Join(dir, "systemctl")
	if err := os.WriteFile(file, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$MIO_TEST_SYSTEMCTL_LOG\"\ncase \"$*\" in *is-active*) echo active;; *is-enabled*) echo enabled;; esac\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MIO_TEST_SYSTEMCTL_LOG", log)
	return log
}

func TestCoreServiceInstallLeavesExistingTunnelUntouched(t *testing.T) {
	isolatedEnv(t)
	log := testSystemctlStub(t)
	tunnelUnit, err := service.TunnelUnitPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(tunnelUnit), 0700); err != nil {
		t.Fatal(err)
	}
	const original = "pre-existing OpenAI tunnel unit\n"
	if err := os.WriteFile(tunnelUnit, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range [][]string{{"service", "install"}, {"service", "start"}, {"service", "restart"}, {"service", "status"}} {
		code, out, errOut := run(t, cmd...)
		if code != 0 {
			t.Fatalf("%v: exit=%d out=%s err=%s", cmd, code, out, errOut)
		}
	}
	b, err := os.ReadFile(tunnelUnit)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != original {
		t.Fatalf("Core commands modified tunnel unit: %q", b)
	}
	commands, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(commands), "miodesk-tunnel") {
		t.Fatalf("Core commands operated tunnel: %q", commands)
	}
}

func TestOptionalTunnelServiceRequiresExplicitSetup(t *testing.T) {
	isolatedEnv(t)
	testSystemctlStub(t)
	code, _, stderr := run(t, "tunnel", "service", "install")
	if code != 1 || !strings.Contains(stderr, "not configured") {
		t.Fatalf("got %d stderr %q", code, stderr)
	}
	coreUnit, err := service.UnitPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(coreUnit); !os.IsNotExist(err) {
		t.Errorf("tunnel install affected Core: %v", err)
	}
	code, _, stderr = run(t, "tunnel", "service", "remove")
	if code != 2 || !strings.Contains(stderr, "unknown tunnel service verb") {
		t.Errorf("unknown verb: %d %s", code, stderr)
	}
}

func TestTunnelServiceInstallDoesNotTouchCore(t *testing.T) {
	isolatedEnv(t)
	_ = testSystemctlStub(t)
	cfg := config.Default()
	cfg.Tunnel.Provider = "openai"
	cfg.Tunnel.OpenAI.TunnelID = "tunnel_0123456789abcdef0123456789abcdef"
	key := filepath.Join(t.TempDir(), "runtime-key")
	if err := os.WriteFile(key, []byte("nonsecret-test-value"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.Tunnel.OpenAI.RuntimeKeyFile = key
	dir := t.TempDir()
	cfg.Tunnel.OpenAI.ProfileDir = dir
	exe := filepath.Join(t.TempDir(), "tunnel-client")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cfg.Tunnel.OpenAI.ClientPath = exe
	if err := os.WriteFile(filepath.Join(dir, "miodesk.yaml"), []byte("test only"), 0600); err != nil {
		t.Fatal(err)
	}
	path, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	coreUnit, err := service.UnitPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(coreUnit), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(coreUnit, []byte("untouched core"), 0600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := run(t, "tunnel", "service", "install")
	if code != 0 {
		t.Fatalf("install: %d, %q, %q", code, out, errOut)
	}
	tunnelUnit, err := service.TunnelUnitPath()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(tunnelUnit)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "Requires=miodesk.service") {
		t.Errorf("new tunnel unit has hard dependency: %s", data)
	}
	old, err := os.ReadFile(coreUnit)
	if err != nil {
		t.Fatal(err)
	}
	if string(old) != "untouched core" {
		t.Fatalf("tunnel service install modified Core: %q", old)
	}
}

func TestNewConfigurationDoesNotRequireTunnel(t *testing.T) {
	isolatedEnv(t)
	cfg := config.Default()
	if cfg.Tunnel.Provider != "local" {
		t.Fatal("fresh Core configuration must default to local")
	}
	code, out, errOut := run(t, "connect")
	if code != 2 || !strings.Contains(errOut, "miodesk serve") {
		t.Fatalf("fresh connect unexpectedly launches tunnel: code=%d out=%q err=%q", code, out, errOut)
	}
}

func TestCoreSetupDoesNotRequireOrRewriteTunnelProfile(t *testing.T) {
	isolatedEnv(t)
	cfg := config.Default()
	cfg.Tunnel.Provider = "openai"
	cfg.Tunnel.OpenAI.TunnelID = "tunnel_0123456789abcdef0123456789abcdef"
	cfg.Workspace.Root = t.TempDir()
	profileDir := t.TempDir()
	cfg.Tunnel.OpenAI.ProfileDir = profileDir
	cfg.Tunnel.OpenAI.RuntimeKeyFile = filepath.Join(t.TempDir(), "no-key-installed")
	profile := filepath.Join(profileDir, "miodesk.yaml")
	if err := os.WriteFile(profile, []byte("original profile"), 0600); err != nil {
		t.Fatal(err)
	}
	path, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := run(t, "setup", "--port", "9900")
	if code != 0 || !strings.Contains(out, "tunnel profile") {
		t.Fatalf("Core setup must not need absent tunnel credentials: code=%d out=%s err=%s", code, out, errOut)
	}
	b, err := os.ReadFile(profile)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "original profile" {
		t.Errorf("Core setup changed OpenAI profile: %q", b)
	}
	if !strings.Contains(out, "update the separate tunnel profile") {
		t.Errorf("port-change warning missing: %s", out)
	}
	cfg, err = config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tunnel.Provider != "openai" || cfg.Server.Port != 9900 {
		t.Errorf("preserve existing OpenAI configuration: %+v", cfg)
	}
}

func TestExplicitTunnelSetupRefreshesWithoutCoreChanges(t *testing.T) {
	isolatedEnv(t)
	cfg := config.Default()
	cfg.Tunnel.Provider = "openai"
	cfg.Tunnel.OpenAI.TunnelID = "tunnel_0123456789abcdef0123456789abcdef"
	cfg.Workspace.Root = t.TempDir()
	cfg.Server.Port = 9900
	key := filepath.Join(t.TempDir(), "runtime-key")
	if err := os.WriteFile(key, []byte("fixture, not real API key"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.Tunnel.OpenAI.RuntimeKeyFile = key
	dir := t.TempDir()
	cfg.Tunnel.OpenAI.ProfileDir = dir
	exe := filepath.Join(t.TempDir(), "tunnel-client")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" > \"$MIO_TEST_PROFILE_LOG\"\nprintf refreshed > \"$MIO_TEST_PROFILE_DIR/miodesk.yaml\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "call-log")
	t.Setenv("MIO_TEST_PROFILE_LOG", log)
	t.Setenv("MIO_TEST_PROFILE_DIR", dir)
	cfg.Tunnel.OpenAI.ClientPath = exe
	path, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := run(t, "tunnel", "setup")
	if code != 0 || !strings.Contains(out, "profile refreshed") {
		t.Fatalf("explicit refresh: code=%d out=%q err=%q", code, out, errOut)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "http://127.0.0.1:9900/mcp") {
		t.Errorf("wrong MCP target: %s", b)
	}
	coreUnit, err := service.UnitPath()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(coreUnit); !os.IsNotExist(err) {
		t.Errorf("tunnel setup unexpectedly installed Core unit: %v", err)
	}
}
