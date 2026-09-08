package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDefaultReleaseManifestURL(t *testing.T) {
	const want = "https://github.com/Chillizu/miodesk/releases/latest/download/manifest.json"
	if defaultReleaseManifestURL != want {
		t.Fatalf("defaultReleaseManifestURL = %q, want %q", defaultReleaseManifestURL, want)
	}
}

func TestUpdateCheckUsesDefaultReleaseManifest(t *testing.T) {
	isolatedEnv(t)
	hits := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"version":"0.2.1-dev","assets":{}}`)
	}))
	defer server.Close()

	old := releaseManifestURL
	releaseManifestURL = server.URL
	defer func() { releaseManifestURL = old }()

	code, out, errOut := run(t, "update", "--check")
	if code != 0 {
		t.Fatalf("update --check exit = %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	if hits != 1 {
		t.Fatalf("default manifest requests = %d, want 1", hits)
	}
}

func TestUpdateCheckFromOverridesDefaultReleaseManifest(t *testing.T) {
	isolatedEnv(t)
	defaultHits := 0
	defaultServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		defaultHits++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"version":"0.2.1-dev","assets":{}}`)
	}))
	defer defaultServer.Close()

	overrideHits := 0
	overrideServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		overrideHits++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"version":"0.2.1-dev","assets":{}}`)
	}))
	defer overrideServer.Close()

	old := releaseManifestURL
	releaseManifestURL = defaultServer.URL
	defer func() { releaseManifestURL = old }()

	code, out, errOut := run(t, "update", "--check", "--from", overrideServer.URL)
	if code != 0 {
		t.Fatalf("update --check --from exit = %d\nstdout:\n%s\nstderr:\n%s", code, out, errOut)
	}
	if defaultHits != 0 || overrideHits != 1 {
		t.Fatalf("manifest requests: default=%d override=%d, want default=0 override=1", defaultHits, overrideHits)
	}
}
