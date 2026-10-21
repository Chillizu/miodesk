package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"

	"github.com/Chillizu/miodesk/internal/config"
	"github.com/Chillizu/miodesk/internal/service"
)

// preflightReport deliberately contains no token values or configuration
// contents. It works before 'setup' and never writes to the filesystem.
type preflightReport struct {
	Kind                 string `json:"kind"`
	OS                   string `json:"os"`
	Architecture         string `json:"architecture"`
	ReleaseAsset         string `json:"release_asset,omitempty"`
	SupportedRelease     bool   `json:"supported_release"`
	ConfigExists         bool   `json:"config_exists"`
	CoreSystemdSupported bool   `json:"core_systemd_supported"`
	TunnelClientFound    bool   `json:"tunnel_client_found"`
	LocalMCPURL          string `json:"local_mcp_url"`
	Next                 string `json:"next"`
}

func currentReleaseAsset(osName, arch string) string {
	switch osName {
	case "linux", "darwin":
		if arch == "amd64" || arch == "arm64" {
			return "miodesk-" + osName + "-" + arch
		}
	case "windows":
		if arch == "amd64" {
			return "miodesk-windows-amd64.exe"
		}
	}
	return ""
}

func runPreflight(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("preflight", stderr)
	jsonFlag := fs.Bool("json", false, "print machine-readable JSON (no secrets)")
	if help, err := parseFlags(fs, args); help {
		return 0
	} else if err != nil {
		return 2
	}
	if !requireNoPositional(fs, stderr) {
		return 2
	}
	cfgPath, err := config.Path()
	if err != nil {
		errf(stderr, "resolve configuration directory: %v", err)
		return 1
	}
	info, err := os.Stat(cfgPath)
	if err != nil && !os.IsNotExist(err) {
		errf(stderr, "inspect configuration: %v", err)
		return 1
	}
	_, tunnelErr := exec.LookPath("tunnel-client")
	asset := currentReleaseAsset(runtime.GOOS, runtime.GOARCH)
	next := "miodesk setup --non-interactive --workspace <absolute-existing-directory>"
	if asset == "" {
		next = "No prebuilt release asset for this platform; check the project's release page before proceeding."
	}
	report := preflightReport{
		Kind:                 "preflight",
		OS:                   runtime.GOOS,
		Architecture:         runtime.GOARCH,
		ReleaseAsset:         asset,
		SupportedRelease:     asset != "",
		ConfigExists:         err == nil && !info.IsDir(),
		CoreSystemdSupported: service.Supported(),
		TunnelClientFound:    tunnelErr == nil,
		LocalMCPURL:          "http://127.0.0.1:8787/mcp",
		Next:                 next,
	}
	if *jsonFlag {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			errf(stderr, "write JSON: %v", err)
			return 1
		}
		return 0
	}
	okf(stdout, "platform: %s/%s", report.OS, report.Architecture)
	if asset != "" {
		okf(stdout, "release asset: %s", asset)
	} else {
		warnf(stdout, "no official binary for this platform")
	}
	infof(stdout, "configuration exists: %t", report.ConfigExists)
	infof(stdout, "official tunnel-client detected: %t", report.TunnelClientFound)
	if report.CoreSystemdSupported {
		infof(stdout, "persistent systemd service: OS supports it; verify user systemd manager before opting in")
	} else {
		infof(stdout, "built-in systemd service: unavailable on this platform")
	}
	fmt.Fprintf(stdout, "Next: %s\n", report.Next)
	return 0
}
