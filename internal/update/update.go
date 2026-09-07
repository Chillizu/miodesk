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
	"net"
	"net/http"
	"net/url"
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
// The sha256 field is optional for version checks, but required before an
// update is installed.
type Manifest struct {
	Version string           `json:"version"`
	Assets  map[string]Asset `json:"assets"`
}

// Asset points at one platform binary.
type Asset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256,omitempty"`
}

const (
	// Keep remote metadata and binaries bounded even when a feed endpoint is
	// reachable but malicious or misconfigured.
	maxManifestBytes = 1 << 20
	maxUpdateBytes   = 64 << 20
)

// Platform is the manifest key for the running build.
func Platform() string { return runtime.GOOS + "/" + runtime.GOARCH }

// Fetch downloads and parses the manifest.
func Fetch(feedURL string) (*Manifest, error) {
	if err := validateHTTPURL(feedURL, "release manifest"); err != nil {
		return nil, err
	}
	client := releaseHTTPClient(30*time.Second, "release manifest")
	resp, err := client.Get(feedURL)
	if err != nil {
		return nil, fmt.Errorf("fetch release manifest: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch release manifest: http %d from %s", resp.StatusCode, feedURL)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxManifestBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read release manifest: %w", err)
	}
	if int64(len(data)) > maxManifestBytes {
		return nil, fmt.Errorf("release manifest exceeds %d bytes", maxManifestBytes)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
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
	if _, err := normalizeSHA256(asset.SHA256); err != nil {
		return nil, fmt.Errorf("release %s asset for %s: %w", m.Version, Platform(), err)
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

// replaceBinary downloads url, verifies its required sha256, and swaps it in
// over target via temp file + rename.
func replaceBinary(target, url, wantSHA string) (int64, error) {
	if err := validateHTTPURL(url, "release asset"); err != nil {
		return 0, err
	}
	wantSHA, err := normalizeSHA256(wantSHA)
	if err != nil {
		return 0, err
	}
	client := releaseHTTPClient(5*time.Minute, "release asset")
	resp, err := client.Get(url)
	if err != nil {
		return 0, fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("download: http %d from %s", resp.StatusCode, url)
	}
	if resp.ContentLength > maxUpdateBytes {
		return 0, fmt.Errorf("download exceeds %d bytes", maxUpdateBytes)
	}

	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".miodesk-update-*")
	if err != nil {
		return 0, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(resp.Body, maxUpdateBytes+1))
	if err != nil {
		tmp.Close()
		return 0, fmt.Errorf("download: %w", err)
	}
	if n > maxUpdateBytes {
		_ = tmp.Close()
		return 0, fmt.Errorf("download exceeds %d bytes", maxUpdateBytes)
	}
	if err := tmp.Close(); err != nil {
		return 0, err
	}
	got := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(got, wantSHA) {
		return 0, fmt.Errorf("checksum mismatch: got %s, want %s (the download was not installed)", got, wantSHA)
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

func normalizeSHA256(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if len(value) != sha256.Size*2 {
		return "", fmt.Errorf("release asset sha256 checksum must be a 64-character hexadecimal value")
	}
	if _, err := hex.DecodeString(value); err != nil {
		return "", fmt.Errorf("release asset sha256 checksum is not hexadecimal: %w", err)
	}
	return value, nil
}

func releaseHTTPClient(timeout time.Duration, label string) *http.Client {
	return &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("%s redirect limit exceeded", label)
			}
			return validateHTTPURL(req.URL.String(), label)
		},
	}
}

// validateHTTPURL prevents a release feed or asset from silently downgrading
// to cleartext on a remote host. Loopback HTTP remains available for local
// development and the package's httptest coverage; production feeds/assets
// must use HTTPS and may not embed credentials in the URL.
func validateHTTPURL(raw, label string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return fmt.Errorf("%s URL must be an HTTPS endpoint", label)
	}
	if strings.EqualFold(u.Scheme, "https") {
		return nil
	}
	if strings.EqualFold(u.Scheme, "http") && isLoopbackHost(u.Hostname()) {
		return nil
	}
	return fmt.Errorf("%s URL must use HTTPS (HTTP is allowed only for loopback development endpoints)", label)
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Newer reports whether latest is a strictly newer semantic version than
// current. Comparison is numeric on up to three parts with an optional "v"
// prefix; a release is newer than a pre-release with the same base version.
// Anything unparseable is treated as not newer (predictable, no surprises
// from odd tags).
func Newer(latest, current string) bool {
	l, lPre, ok1 := parseVersion(latest)
	c, cPre, ok2 := parseVersion(current)
	if !ok1 || !ok2 {
		return false
	}
	for i := 0; i < 3; i++ {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return cPre && !lPre
}

func parseVersion(v string) ([3]int, bool, bool) {
	var out [3]int
	preRelease := false
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.Split(v, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return out, false, false
	}
	for i, p := range parts {
		if p == "" {
			return out, false, false
		}
		// Stop at a pre-release/build suffix: 1.2.0-rc1 → 1.2.0. A
		// hyphen marks a pre-release; a plus suffix is only build metadata.
		num := p
		if j := strings.IndexAny(p, "-+"); j >= 0 {
			if i < len(parts)-1 {
				return out, false, false // suffix in the middle is malformed
			}
			preRelease = p[j] == '-'
			num = p[:j]
		}
		n, err := strconv.Atoi(num)
		if err != nil || n < 0 {
			return out, false, false
		}
		out[i] = n
	}
	return out, preRelease, true
}
