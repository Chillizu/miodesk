package update

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		latest, current string
		want            bool
	}{
		{"0.2.0", "0.1.0", true},
		{"0.1.0", "0.1.0", false},
		{"0.1.0", "0.2.0", false},
		{"1.0", "0.9.9", true},
		{"v0.3.0", "0.2.5", true},
		{"0.2.0-rc1", "0.1.9", true},  // suffix ignored, 0.2.0 > 0.1.9
		{"0.2.0", "0.2.0-dev", true},  // release is newer than same-base dev build
		{"0.2.0-rc1", "0.2.0", false}, // a pre-release is not newer than release
		{"0.2.0+build1", "0.2.0", false},
		{"banana", "0.1.0", false}, // unparseable → never newer
		{"0.1.0", "banana", false},
		{"", "0.1.0", false},
	}
	for _, tc := range cases {
		if got := Newer(tc.latest, tc.current); got != tc.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", tc.latest, tc.current, got, tc.want)
		}
	}
}

func fakeSHA(t *testing.T, data []byte) string {
	t.Helper()
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestReplaceBinary(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "miodesk")
	if err := os.WriteFile(target, []byte("OLD BINARY"), 0o755); err != nil {
		t.Fatal(err)
	}

	newBytes := []byte("NEW BINARY CONTENT")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(newBytes)
	}))
	defer srv.Close()

	n, err := replaceBinary(target, srv.URL, fakeSHA(t, newBytes))
	if err != nil {
		t.Fatalf("replaceBinary: %v", err)
	}
	if int64(len(newBytes)) != n {
		t.Errorf("bytes = %d", n)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(newBytes) {
		t.Errorf("content = %q", got)
	}
	fi, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, want executable", fi.Mode().Perm())
	}
	// No temp leftovers.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("leftover files: %v", entries)
	}
}

func TestReplaceBinaryChecksumMismatch(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "miodesk")
	if err := os.WriteFile(target, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("TAMPERED"))
	}))
	defer srv.Close()

	_, err := replaceBinary(target, srv.URL, strings.Repeat("0", 64))
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("error = %v, want checksum mismatch", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "OLD" {
		t.Errorf("target must be untouched on mismatch, got %q", got)
	}
}

func TestReplaceBinaryRequiresChecksum(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "miodesk")
	if err := os.WriteFile(target, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("NEW"))
	}))
	defer srv.Close()

	if _, err := replaceBinary(target, srv.URL, ""); err == nil || !strings.Contains(err.Error(), "64-character") {
		t.Fatalf("missing checksum error = %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "OLD" {
		t.Errorf("target must be untouched when checksum is missing, got %q", got)
	}
}

func TestCheckAgainstFeed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := Manifest{Version: "9.9.9", Assets: map[string]Asset{
			Platform(): {URL: "http://example.invalid/bin"},
		}}
		json.NewEncoder(w).Encode(m)
	}))
	defer srv.Close()

	out, err := Check("0.1.0", srv.URL)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if out.UpToDate || out.Latest != "9.9.9" || out.Current != "0.1.0" {
		t.Errorf("outcome = %+v", out)
	}

	out, err = Check("9.9.9", srv.URL)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !out.UpToDate {
		t.Errorf("same version should be up to date: %+v", out)
	}
}

func TestUpdateMissingPlatformAsset(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := Manifest{Version: "9.9.9", Assets: map[string]Asset{
			"os/999": {URL: "http://example.invalid/bin"},
		}}
		json.NewEncoder(w).Encode(m)
	}))
	defer srv.Close()

	if _, err := Update("0.1.0", srv.URL); err == nil {
		t.Fatal("missing platform asset must error")
	} else if !strings.Contains(err.Error(), "no asset for") {
		t.Errorf("error = %v", err)
	}
}

func TestUpdateRequiresChecksum(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m := Manifest{Version: "9.9.9", Assets: map[string]Asset{
			Platform(): {URL: "http://127.0.0.1:1/miodesk"},
		}}
		json.NewEncoder(w).Encode(m)
	}))
	defer srv.Close()

	if _, err := Update("0.1.0", srv.URL); err == nil || !strings.Contains(err.Error(), "64-character") {
		t.Fatalf("missing asset checksum error = %v", err)
	}
}

func TestFetchRejectsOversizedManifest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", maxManifestBytes+1)))
	}))
	defer srv.Close()

	if _, err := Fetch(srv.URL); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized manifest error = %v", err)
	}
}

func TestReplaceBinaryRejectsOversizedDownload(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "miodesk")
	if err := os.WriteFile(target, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "67108865")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if _, err := replaceBinary(target, srv.URL, strings.Repeat("0", 64)); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized download error = %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "OLD" {
		t.Errorf("target must be untouched for oversized download, got %q", got)
	}
}

func TestFetchErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	if _, err := Fetch(srv.URL); err == nil {
		t.Fatal("404 manifest must error")
	}

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"version": ""}`))
	}))
	defer srv2.Close()
	if _, err := Fetch(srv2.URL); err == nil {
		t.Fatal("manifest without version must error")
	}
}

func TestRemoteUpdateURLsRequireHTTPS(t *testing.T) {
	if _, err := Fetch("http://example.com/releases.json"); err == nil {
		t.Fatal("remote HTTP feed must be rejected")
	} else if !strings.Contains(err.Error(), "HTTPS") {
		t.Errorf("error = %v", err)
	}
	if err := validateHTTPURL("http://127.0.0.1:1234/feed", "release manifest"); err != nil {
		t.Errorf("loopback HTTP should remain available for local development: %v", err)
	}
	if err := validateHTTPURL("https://releases.example.com/feed", "release manifest"); err != nil {
		t.Errorf("HTTPS should be accepted: %v", err)
	}
}
