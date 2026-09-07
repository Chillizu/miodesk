package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDefault(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	cfg := Default()
	if cfg.Server.Host != "127.0.0.1" || cfg.Server.Port != 0 {
		t.Errorf("server defaults = %+v", cfg.Server)
	}
	if cfg.Workspace.Root != "/home/u" {
		t.Errorf("workspace root = %q, want /home/u", cfg.Workspace.Root)
	}
	if cfg.Tunnel.Provider != "auto" {
		t.Errorf("tunnel provider = %q", cfg.Tunnel.Provider)
	}
	if cfg.Widget.Theme != "auto" {
		t.Errorf("widget theme = %q", cfg.Widget.Theme)
	}
	if cfg.Logging.Level != "info" || cfg.Logging.Format != "text" {
		t.Errorf("logging defaults = %+v", cfg.Logging)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("default config should validate: %v", err)
	}
}

func TestParseSample(t *testing.T) {
	sample := `
[server]
host = "127.0.0.1"
port = 8787

[workspace]
root = "/home/user/project"

[tunnel]
provider = "cloudflare"

[widget]
theme = "dark"
token_stats = "bottom"
details = "expanded"
`
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, existed, err := LoadOrDefault(path)
	if err != nil || !existed {
		t.Fatalf("LoadOrDefault: existed=%v err=%v", existed, err)
	}
	if cfg.Server.Port != 8787 {
		t.Errorf("port = %d", cfg.Server.Port)
	}
	if cfg.Workspace.Root != "/home/user/project" {
		t.Errorf("root = %q", cfg.Workspace.Root)
	}
	if cfg.Tunnel.Provider != "cloudflare" {
		t.Errorf("provider = %q", cfg.Tunnel.Provider)
	}
	if cfg.Widget.Theme != "dark" {
		t.Errorf("theme = %q", cfg.Widget.Theme)
	}
}

func TestPartialFileFillsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[server]\nport = 9000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Host != "127.0.0.1" {
		t.Errorf("host = %q, want default", cfg.Server.Host)
	}
	if cfg.Workspace.Root == "" {
		t.Error("workspace root should fall back to default, not empty")
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "missing.toml")); err == nil {
		t.Fatal("Load on missing file should error")
	}
	cfg, existed, err := LoadOrDefault(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil || existed {
		t.Fatalf("LoadOrDefault on missing file: existed=%v err=%v", existed, err)
	}
	if cfg.Server.Port != 0 {
		t.Errorf("port = %d, want default 0", cfg.Server.Port)
	}
}

func TestInvalidTOMLErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[server\nport = oops"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadOrDefault(path); err == nil {
		t.Fatal("expected parse error")
	} else if !strings.Contains(err.Error(), "parse") {
		t.Errorf("error should mention the file: %v", err)
	}
}

func TestSaveLoadRoundtrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.toml")
	cfg := Default()
	cfg.Server.Port = 8787
	cfg.Workspace.Root = "/tmp/ws"
	cfg.Tunnel.Provider = "tailscale"
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Server.Port != 8787 || got.Workspace.Root != "/tmp/ws" || got.Tunnel.Provider != "tailscale" {
		t.Errorf("roundtrip mismatch: %+v", got)
	}
}

func TestSaveUsesPrivateAtomicConfigFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not provide Unix permission semantics")
	}

	dir := filepath.Join(t.TempDir(), "config-home")
	path := filepath.Join(dir, "config.toml")
	cfg := Default()
	cfg.Remote.Mode = "token"
	cfg.Remote.Token = "private-token"
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}

	if got := filePerm(t, dir); got != 0o700 {
		t.Errorf("config directory mode = %o, want 700", got)
	}
	if got := filePerm(t, path); got != 0o600 {
		t.Errorf("config file mode = %o, want 600", got)
	}

	// Saving again must also repair a legacy world-readable config.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.Remote.Token = "updated-private-token"
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	if got := filePerm(t, path); got != 0o600 {
		t.Errorf("rewritten config mode = %o, want 600", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "updated-private-token") {
		t.Errorf("rewritten config missing updated token: %s", data)
	}
}

func filePerm(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

func TestValidateRejectsBadValues(t *testing.T) {
	cfg := Default()
	cfg.Server.Port = -1
	if err := cfg.Validate(); err == nil {
		t.Error("negative port should be rejected")
	}
	cfg = Default()
	cfg.Widget.Theme = "midnight"
	if err := cfg.Validate(); err == nil {
		t.Error("unknown theme should be rejected")
	}
	cfg = Default()
	cfg.Tunnel.Provider = "heroku"
	if err := cfg.Validate(); err == nil {
		t.Error("unknown provider should be rejected")
	}
}

func TestRemoteValidation(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default remote config should validate: %v", err)
	}
	cfg.Remote.Mode = "banana"
	if err := cfg.Validate(); err == nil {
		t.Error("unknown remote.mode should be rejected")
	}
	cfg.Remote.Mode = "token"
	if err := cfg.Validate(); err == nil {
		t.Error("token mode without token should be rejected")
	}
	cfg.Remote.Token = "x"
	if err := cfg.Validate(); err != nil {
		t.Errorf("token mode with token should validate: %v", err)
	}
}

func TestLoggingValidation(t *testing.T) {
	cfg := Default()
	cfg.Logging.Level = "trace"
	if err := cfg.Validate(); err == nil {
		t.Error("unknown logging.level should be rejected")
	}
	cfg = Default()
	cfg.Logging.Format = "yaml"
	if err := cfg.Validate(); err == nil {
		t.Error("unknown logging.format should be rejected")
	}
}

func TestWidgetDeadConfigRemoved(t *testing.T) {
	// widget.token_stats / widget.details were removed; unknown keys in the
	// widget table must not silently persist as config.
	cfg := Default()
	cfg.Widget.Theme = "dark"
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if strings.Contains(string(data), "token_stats") || strings.Contains(string(data), "details") {
		t.Errorf("dead widget config keys persisted:\n%s", data)
	}
}
