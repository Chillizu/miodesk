// Package xdg resolves miodesk's runtime directories following the XDG Base
// Directory Specification (https://specifications.freedesktop.org/basedir-spec/latest/).
//
// Each directory is $XDG_<ROLE>_HOME/miodesk when that environment variable is
// set to an absolute path, otherwise $HOME/<fallback>/miodesk.
package xdg

import (
	"fmt"
	"os"
	"path/filepath"
)

// ConfigDir holds config.toml. $XDG_CONFIG_HOME, fallback ~/.config.
func ConfigDir() (string, error) { return resolve("XDG_CONFIG_HOME", ".config") }

// DataDir holds persistent data. $XDG_DATA_HOME, fallback ~/.local/share.
func DataDir() (string, error) { return resolve("XDG_DATA_HOME", ".local/share") }

// StateDir holds runtime state such as the server status file.
// $XDG_STATE_HOME, fallback ~/.local/state.
func StateDir() (string, error) { return resolve("XDG_STATE_HOME", ".local/state") }

// CacheDir holds disposable caches. $XDG_CACHE_HOME, fallback ~/.cache.
func CacheDir() (string, error) { return resolve("XDG_CACHE_HOME", ".cache") }

func resolve(env, fallback string) (string, error) {
	if v := os.Getenv(env); v != "" {
		if !filepath.IsAbs(v) {
			return "", fmt.Errorf("$%s must be an absolute path, got %q", env, v)
		}
		return filepath.Join(v, "miodesk"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory (set $HOME or $%s)", env)
	}
	return filepath.Join(home, fallback, "miodesk"), nil
}
