package doctor

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/Chillizu/miodesk/internal/config"
	"github.com/Chillizu/miodesk/internal/server"
)

func TestCheckPortIdentifiesMiodesk(t *testing.T) {
	tests := []struct {
		name       string
		healthHead bool
		want       Status
	}{
		{name: "miodesk", healthHead: true, want: StatusOK},
		{name: "unrelated server", want: StatusWarn},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/healthz" {
					http.NotFound(w, r)
					return
				}
				if tt.healthHead {
					w.Header().Set(server.HealthHeader, server.HealthHeaderValue)
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer ts.Close()

			u, err := url.Parse(ts.URL)
			if err != nil {
				t.Fatal(err)
			}
			port, err := strconv.Atoi(u.Port())
			if err != nil {
				t.Fatal(err)
			}
			cfg := &config.Config{Server: config.Server{Host: u.Hostname(), Port: port}}
			var report Report
			checkPort(&report, cfg)
			if len(report.Checks) != 1 || report.Checks[0].Status != tt.want {
				t.Fatalf("checkPort() = %+v, want status %s", report.Checks, tt.want)
			}
		})
	}
}

func TestDoctorCanCheckCoreWithInvalidOptionalTunnel(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Default()
	cfg.Workspace.Root = t.TempDir()
	cfg.Server.Port = 0
	cfg.Tunnel.Provider = "typo-provider"
	report := Run("test-config.toml", cfg, nil)
	if report.HasErrors() {
		t.Fatalf("optional tunnel misconfiguration must not make Core unusable: %+v", report.Checks)
	}
	seenCore := false
	seenTunnelWarning := false
	for _, item := range report.Checks {
		if item.Name == "mcp" && item.Status == StatusOK {
			seenCore = true
		}
		if item.Name == "tunnel" && item.Status == StatusWarn {
			seenTunnelWarning = true
		}
	}
	if !seenCore || !seenTunnelWarning {
		t.Errorf("Core and optional tunnel diagnostics should be independent: %+v", report.Checks)
	}
}
