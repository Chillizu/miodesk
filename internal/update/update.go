// Package update implements miodesk's self-update: a version check against a
// release manifest and a safe, atomic binary replacement. No package manager,
// no daemon, no auto-start tricks — and a hash mismatch never touches disk.
package update

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Manifest is the release feed contract. A feed is a JSON file:
//
//	{"version": "0.3.0",
//	 "assets": {"linux/amd64": {"url": "https://…/miodesk", "sha256": "…"}, …}}
//
// The sha256 field is optional but strongly recommended.
type Manifest struct {
	Version string           `json:"version"`
	Assets  map[string]Asset `json:"assets"`
}

// Asset points at one platform binary.
type Asset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256,omitempty"`
}

// Platform is the manifest key for the running build.
func Platform() string { return runtime.GOOS + "/" + runtime.GOARCH }

// Fetch downloads and parses the manifest.
func Fetch(feedURL string) (*Manifest, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(feedURL)
	if err != nil {
		return nil, fmt.Errorf("fetch release manifest: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch release manifest: http %d from %s", resp.StatusCode, feedURL)
	}
	var m Manifest
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, fmt.Errorf("parse release manifest: %w", err)
	}
	if m.Version == "" {
		return nil, fmt.Errorf("release manifest has no version")
	}
	return &m, nil
}

// Outcome summarizes one update run for the CLI to print.
type Outcome struct {
	Current  string
	Latest   string
	UpToDate bool
	Applied  bool
	Replaced string // path of the replaced binary
	Bytes    int64
}

// Check compares the current version with the manifest. It never writes.
func Check(current, feedURL string) (*Outcome, error) {
	m, err := Fetch(feedURL)
	if err != nil {
		return nil, err
	}
	return &Outcome{Current: current, Latest: m.Version, UpToDate: !Newer(m.Version, current)}, nil
}

// Update performs the full cycle: check, download, verify, atomically replace
// the running binary.
func Update(current, feedURL string) (*Outcome, error) {
	m, err := Fetch(feedURL)
	if err != nil {
		return nil, err
	}
	if !Newer(m.Version, current) {
		return &Outcome{Current: current, Latest: m.Version, UpToDate: true}, nil
	}
	asset, ok := m.Assets[Platform()]
	if !ok {
		return nil, fmt.Errorf("release %s has no asset for %s", m.Version, Platform())
	}

	bin, err := os.Executable()
	if err != nil {
		return nil, err
	}
	bin, err = filepath.EvalSymlinks(bin)
	if err != nil {
		return nil, err
	}
	n, err := replaceBinary(bin, asset.URL, asset.SHA256)
	if err != nil {
		return nil, err
	}
	return &Outcome{Current: current, Latest: m.Version, Applied: true, Replaced: bin, Bytes: n}, nil
}

// replaceBinary downloads url, optionally verifies sha256, and swaps it in
// over target via temp file + rename.
func replaceBinary(target, url, wantSHA string) (int64, error) {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return 0, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("download: http %d from %s", resp.StatusCode, url)
	}

	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".miodesk-update-*")
	if err != nil {
		return 0, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, hash), resp.Body)
	if err != nil {
		tmp.Close()
		return 0, fmt.Errorf("download: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return 0, err
	}
	if wantSHA != "" {
		got := hex.EncodeToString(hash.Sum(nil))
		if !strings.EqualFold(got, wantSHA) {
			return 0, fmt.Errorf("checksum mismatch: got %s, want %s (the download was not installed)", got, wantSHA)
		}
	}
	fi, err := os.Stat(target)
	if err == nil {
		if err := os.Chmod(tmpName, fi.Mode()); err != nil {
			return 0, err
		}
	} else if err := os.Chmod(tmpName, 0o755); err != nil {
		return 0, err
	}
	if err := os.Rename(tmpName, target); err != nil {
		return 0, err
	}
	return n, nil
}

// Newer reports whether latest is a strictly newer semantic version than
// current. Comparison is numeric on up to three parts with an optional "v"
// prefix; anything unparseable is treated as not newer (predictable, no
// surprises from odd tags).
func Newer(latest, current string) bool {
	l, ok1 := parseVersion(latest)
	c, ok2 := parseVersion(current)
	if !ok1 || !ok2 {
		return false
	}
	for i := 0; i < 3; i++ {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.Split(v, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return out, false
	}
	for i, p := range parts {
		if p == "" {
			return out, false
		}
		// Stop at a pre-release/build suffix: 1.2.0-rc1 → 1.2.0.
		num := p
		if j := strings.IndexAny(p, "-+"); j >= 0 {
			if i < len(parts)-1 {
				return out, false // suffix in the middle is malformed
			}
			num = p[:j]
		}
		n, err := strconv.Atoi(num)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
