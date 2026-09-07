// Package config defines miodesk's config.toml: loading, defaults, and
// validation. Runtime state never belongs in a Config value — it lives in the
// XDG state directory instead.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"

	"github.com/Chillizu/miodesk/internal/xdg"
)

const (
	// DefaultPort is intentionally stable: the OpenAI tunnel-client profile
	// points at the local MCP endpoint and should survive restarts unchanged.
	DefaultPort = 8787
	// DefaultOpenAIProfile is the profile name used by `miodesk setup`.
	DefaultOpenAIProfile = "miodesk"
)

type Server struct {
	// Host to bind. "127.0.0.1" keeps the MCP server local; use 0.0.0.0 only
	// when a tunnel or reverse proxy fronts it.
	Host string `toml:"host"`
	// Port 0 means "pick a random free port on each start". A fixed default
	// keeps the OpenAI tunnel-client's local MCP target stable across restarts.
	Port int `toml:"port"`
}

type Workspace struct {
	// Root is the sandbox boundary for every file and command tool.
	Root string `toml:"root"`
}

type Tunnel struct {
	// Provider is openai by default. The legacy providers remain accepted for
	// existing configurations, but are intentionally not part of the primary
	// onboarding path.
	Provider string       `toml:"provider"`
	OpenAI   OpenAITunnel `toml:"openai"`
}

// OpenAITunnel contains non-secret references used to prepare the external
// tunnel-client. The runtime API key itself stays in RuntimeKeyFile and is
// never copied into this config or passed as a literal argument.
type OpenAITunnel struct {
	TunnelID       string `toml:"tunnel_id"`
	RuntimeKeyFile string `toml:"runtime_key_file"`
	Profile        string `toml:"profile"`
	ProfileDir     string `toml:"profile_dir"`
	ClientPath     string `toml:"client_path"`
}

type Widget struct {
	Theme string `toml:"theme"` // auto | light | dark
}

// Logging controls diagnostic records emitted by the server and CLI. The
// default stderr sink is intentional: the systemd user service collects it in
// journald, while foreground runs keep the same records visible in a terminal.
type Logging struct {
	Level  string `toml:"level"`  // debug | info | warn | error
	Format string `toml:"format"` // text | json
}

type Remote struct {
	// Mode is the remote-access trust level:
	//   "" / "local" — no token; the server must only be reachable from the
	//                  local machine (loopback bind, stdio).
	//   "token"      — every request must present the configured bearer token.
	//   "unsafe"     — no authentication; explicit developer escape hatch.
	// Empty means local; an explicit "local" in the file makes
	// `miodesk connect` refuse to open a remote entrance.
	Mode string `toml:"mode"`
	// Token is the bearer secret for mode "token". It never appears in
	// status, doctor, widget, or error output.
	Token string `toml:"token"`
}

type Config struct {
	Server    Server    `toml:"server"`
	Workspace Workspace `toml:"workspace"`
	Tunnel    Tunnel    `toml:"tunnel"`
	Widget    Widget    `toml:"widget"`
	Logging   Logging   `toml:"logging"`
	Remote    Remote    `toml:"remote"`
}

// Default returns the built-in configuration. The workspace root defaults to
// the user's home directory.
func Default() *Config {
	home, _ := os.UserHomeDir()
	return &Config{
		Server:    Server{Host: "127.0.0.1", Port: DefaultPort},
		Workspace: Workspace{Root: home},
		Tunnel:    Tunnel{Provider: "openai", OpenAI: OpenAITunnel{Profile: DefaultOpenAIProfile}},
		Widget:    Widget{Theme: "auto"},
		Logging:   Logging{Level: "info", Format: "text"},
	}
}

// Path returns the resolved config.toml location.
func Path() (string, error) {
	dir, err := xdg.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.toml"), nil
}

// Load reads and parses path, filling unset fields with defaults.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg := Default()
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.applyDefaults()
	return cfg, nil
}

// LoadOrDefault loads path if it exists; otherwise returns defaults and
// existed=false. A parse error is still an error — a broken config must be
// surfaced, not silently replaced.
func LoadOrDefault(path string) (cfg *Config, existed bool, err error) {
	cfg = Default()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, true, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.applyDefaults()
	return cfg, true, nil
}

// Save writes the config as TOML, creating the parent directory.
func (c *Config) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	// Config may contain a remote bearer token. Harden both newly-created and
	// pre-existing XDG directories before writing it.
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("secure config directory %s: %w", dir, err)
	}
	data, err := toml.Marshal(c)
	if err != nil {
		return err
	}

	// Write a private temporary file and rename it into place so an interrupted
	// save cannot leave a truncated config. CreateTemp starts at 0600, and the
	// explicit chmod also repairs an older 0644 config on every save.
	tmp, err := os.CreateTemp(dir, ".config.toml-*")
	if err != nil {
		return fmt.Errorf("create temporary config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("secure temporary config: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close config: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

func (c *Config) applyDefaults() {
	def := Default()
	if c.Server.Host == "" {
		c.Server.Host = def.Server.Host
	}
	if c.Workspace.Root == "" {
		c.Workspace.Root = def.Workspace.Root
	}
	if c.Tunnel.Provider == "" {
		c.Tunnel.Provider = def.Tunnel.Provider
	}
	if c.Tunnel.OpenAI.Profile == "" {
		c.Tunnel.OpenAI.Profile = def.Tunnel.OpenAI.Profile
	}
	if c.Widget.Theme == "" {
		c.Widget.Theme = def.Widget.Theme
	}
	if c.Logging.Level == "" {
		c.Logging.Level = def.Logging.Level
	}
	if c.Logging.Format == "" {
		c.Logging.Format = def.Logging.Format
	}
}

// Validate reports configuration values miodesk cannot act on.
func (c *Config) Validate() error {
	if c.Server.Port < 0 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port %d out of range 0-65535", c.Server.Port)
	}
	switch c.Widget.Theme {
	case "auto", "light", "dark":
	default:
		return fmt.Errorf("widget.theme %q must be auto, light, or dark", c.Widget.Theme)
	}
	switch c.Logging.Level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("logging.level %q must be debug, info, warn, or error", c.Logging.Level)
	}
	switch c.Logging.Format {
	case "text", "json":
	default:
		return fmt.Errorf("logging.format %q must be text or json", c.Logging.Format)
	}
	switch c.Remote.Mode {
	case "", "local", "token", "unsafe":
	default:
		return fmt.Errorf("remote.mode %q must be local, token, or unsafe", c.Remote.Mode)
	}
	if c.Remote.Mode == "token" && c.Remote.Token == "" {
		return fmt.Errorf("remote.mode is %q but remote.token is empty", c.Remote.Mode)
	}
	switch c.Tunnel.Provider {
	case "openai", "auto", "local", "cloudflare", "ngrok", "tailscale", "custom":
	default:
		return fmt.Errorf("tunnel.provider %q must be openai, auto, local, cloudflare, ngrok, tailscale, or custom", c.Tunnel.Provider)
	}
	return nil
}
