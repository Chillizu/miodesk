package cli

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPreflightBeforeSetupIsSafeJSON(t *testing.T) {
	isolatedEnv(t)
	code, out, errOut := run(t, "preflight", "--json")
	if code != 0 || errOut != "" {
		t.Fatalf("preflight: exit=%d stdout=%q stderr=%q", code, out, errOut)
	}
	var p preflightReport
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if p.Kind != "preflight" || p.OS != runtime.GOOS || p.Architecture != runtime.GOARCH || p.ConfigExists {
		t.Fatalf("bad fresh profile: %+v", p)
	}
	if p.ReleaseAsset != currentReleaseAsset(runtime.GOOS, runtime.GOARCH) {
		t.Fatalf("incorrect asset: %+v", p)
	}
	if strings.Contains(strings.ToLower(out), "api_key") || strings.Contains(out, "sk-proj-") {
		t.Fatalf("preflight leaked apparent secret: %s", out)
	}
}

func TestPreflightAssetMatrix(t *testing.T) {
	for _, tt := range []struct{ os, arch, want string }{
		{"linux", "amd64", "miodesk-linux-amd64"},
		{"linux", "arm64", "miodesk-linux-arm64"},
		{"darwin", "amd64", "miodesk-darwin-amd64"},
		{"darwin", "arm64", "miodesk-darwin-arm64"},
		{"windows", "amd64", "miodesk-windows-amd64.exe"},
		{"windows", "arm64", ""},
		{"freebsd", "amd64", ""},
	} {
		if got := currentReleaseAsset(tt.os, tt.arch); got != tt.want {
			t.Errorf("%s/%s asset = %q, want %q", tt.os, tt.arch, got, tt.want)
		}
	}
}

func TestAgentSetupExplicitNonInteractive(t *testing.T) {
	isolatedEnv(t)
	ws := t.TempDir()
	code, out, errOut := run(t, "setup", "--non-interactive")
	if code != 2 || !strings.Contains(errOut, "requires --workspace") || strings.Contains(out, "configuration ready") {
		t.Fatalf("missing workspace: exit=%d out=%q err=%q", code, out, errOut)
	}
	code, out, errOut = run(t, "setup", "--non-interactive", "--workspace", ws)
	if code != 0 || !strings.Contains(out, "configuration ready:") {
		t.Fatalf("explicit setup failed: exit=%d out=%q err=%q", code, out, errOut)
	}
	code, _, errOut = run(t, "setup", "--non-interactive")
	if code != 0 {
		t.Fatalf("existing setup must remain idempotent: %d %s", code, errOut)
	}
	code, _, errOut = run(t, "setup", "--non-interactive", "--interactive")
	if code != 2 || !strings.Contains(errOut, "cannot be combined") {
		t.Fatalf("conflicting flags not rejected: %d %s", code, errOut)
	}
}

func TestAgentSetupAbsoluteWorkspacePath(t *testing.T) {
	isolatedEnv(t)
	ws := t.TempDir()
	code, out, errOut := run(t, "setup", "--non-interactive", "--workspace", filepath.Clean(ws))
	if code != 0 || strings.Contains(out, "Workspace root [") {
		t.Fatalf("unexpected wizard or setup error: %d %q %q", code, out, errOut)
	}
}
