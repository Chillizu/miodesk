package cli

import "testing"

func TestDefaultReleaseManifestURL(t *testing.T) {
	const want = "https://github.com/Chillizu/miodesk/releases/latest/download/manifest.json"
	if defaultReleaseManifestURL != want {
		t.Fatalf("defaultReleaseManifestURL = %q, want %q", defaultReleaseManifestURL, want)
	}
}
