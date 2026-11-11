package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Chillizu/miodesk/internal/config"
)

func panelTestServer(t *testing.T) (*Server, string, string) {
	t.Helper()
	s := newTestServer(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	tokenFile := panelManagedTokenFile(path)
	if err := os.MkdirAll(filepath.Dir(tokenFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenFile, []byte("private-bridge-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.cfg.Clients.Mioweave.TokenFile = tokenFile
	// Never probe an unrelated service on a developer's default Bridge port.
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/health" {
			_, _ = io.WriteString(w, `{"ok":true,"bridge":"mioweave","token_source":"file","token":"external-secret-must-not-leak"}`)
			return
		}
		if r.URL.Path == "/tools" && r.Header.Get("X-WebMCP-Token") == "private-bridge-token" {
			_, _ = io.WriteString(w, `{"ok":true,"tools":[{"name":"status","token":"external-secret-must-not-leak"}]}`)
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(bridge.Close)
	s.cfg.Clients.Mioweave.URL = bridge.URL
	if err := s.cfg.Save(path); err != nil {
		t.Fatal(err)
	}
	return s, path, tokenFile
}

func panelRequest(t *testing.T, h http.Handler, method, path, body string, change func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "http://127.0.0.1:8787"+path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:12345"
	if method == http.MethodPost {
		r.Header.Set("Origin", "http://127.0.0.1:8787")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Miodesk-Panel", "1")
	}
	if change != nil {
		change(r)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestPanelLocalBoundaryIndependentOfCoreMode(t *testing.T) {
	s, _, _ := panelTestServer(t)
	for _, mode := range []string{"local", "token", "unsafe"} {
		t.Run(mode, func(t *testing.T) {
			s.auth = NewAuthorizeMust(mode, "core-remote-token")
			h := s.Handler()
			for _, tc := range []struct {
				name   string
				change func(*http.Request)
			}{
				{"remote peer", func(r *http.Request) { r.RemoteAddr = "192.0.2.5:443" }},
				{"rebound host", func(r *http.Request) {
					r.Host = "evil.example:8787"
					r.Header.Set("Origin", "http://evil.example:8787")
				}},
				{"host suffix", func(r *http.Request) { r.Host = "localhost.evil.example:8787" }},
				{"evil origin despite bearer", func(r *http.Request) {
					r.Header.Set("Origin", "https://evil.example")
					r.Header.Set("Authorization", "Bearer core-remote-token")
				}},
				{"missing origin", func(r *http.Request) { r.Header.Del("Origin") }},
				{"missing csrf header", func(r *http.Request) { r.Header.Del("X-Miodesk-Panel") }},
				{"form post", func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }},
				{"cross site", func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }},
				{"forwarded", func(r *http.Request) { r.Header.Set("Forwarded", "for=192.0.2.5") }},
				{"proxy origin", func(r *http.Request) { r.Header.Set("X-Forwarded-For", "192.0.2.5") }},
			} {
				t.Run(tc.name, func(t *testing.T) {
					w := panelRequest(t, h, http.MethodPost, "/api/panel/mioweave/token", `{}`, tc.change)
					if w.Code != http.StatusForbidden || strings.Contains(w.Body.String(), "private-bridge-token") {
						t.Fatalf("boundary failed: %d %s", w.Code, w.Body.String())
					}
				})
			}
			w := panelRequest(t, h, http.MethodPost, "/api/panel/mioweave/token", `{}`, nil)
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "private-bridge-token") {
				t.Fatalf("explicit local reveal: %d %s", w.Code, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("credential response must not be cached")
			}
			w = panelRequest(t, h, http.MethodGet, "/api/panel/mioweave/token", "", nil)
			if w.Code != http.StatusMethodNotAllowed || strings.Contains(w.Body.String(), "private-bridge-token") {
				t.Fatalf("GET leaked credential: %d", w.Code)
			}
			w = panelRequest(t, h, http.MethodGet, "/panel/", "", func(r *http.Request) { r.Host = "attacker.example:8787" })
			if w.Code != http.StatusForbidden {
				t.Fatalf("DNS rebound navigation allowed: %d", w.Code)
			}
		})
	}
}

func TestPanelStatusDoesNotExposeSecrets(t *testing.T) {
	s, _, _ := panelTestServer(t)
	s.cfg.Remote.Token = "core-secret-must-not-leak"
	w := panelRequest(t, s.Handler(), http.MethodGet, "/api/panel/status", "", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d %s", w.Code, w.Body.String())
	}
	for _, secret := range []string{"private-bridge-token", "core-secret-must-not-leak", "external-secret-must-not-leak"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("status exposed a local or external credential")
		}
	}
	var status struct {
		Mioweave panelMioweave `json:"mioweave"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Mioweave.Connected || !status.Mioweave.Authorized {
		t.Fatalf("healthy authorized bridge misclassified: %+v", status.Mioweave)
	}
}

func TestPanelTokenUpdatePreservesCredentialsOnRejectedChanges(t *testing.T) {
	s, path, tokenFile := panelTestServer(t)
	h := s.Handler()
	originalConfig, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"https://127.0.0.1:8790", "http://example.com:8790", "http://localhost.evil.test:8790", "http://user:secret@127.0.0.1:8790", "http://127.0.0.1:8790/?token=secret", "http://127.0.0.1:8790/#secret"} {
		body, _ := json.Marshal(map[string]string{"url": address, "token_file": tokenFile, "token": "replacement-token"})
		w := panelRequest(t, h, http.MethodPost, "/api/panel/mioweave", string(body), nil)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("unsafe URL accepted: %s (%d)", address, w.Code)
		}
	}
	body, _ := json.Marshal(map[string]string{"url": s.cfg.Clients.Mioweave.URL, "token_file": filepath.Join(t.TempDir(), "absent-dir", "token"), "token": "replacement-token"})
	w := panelRequest(t, h, http.MethodPost, "/api/panel/mioweave", string(body), nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unwritable credential path: %d %s", w.Code, w.Body.String())
	}
	unchanged, _ := os.ReadFile(path)
	if !bytes.Equal(unchanged, originalConfig) {
		t.Fatal("rejected update modified configuration")
	}
	token, err := readPanelToken(tokenFile)
	if err != nil || token != "private-bridge-token" {
		t.Fatal("rejected update modified credential")
	}

	body, _ = json.Marshal(map[string]string{"url": s.cfg.Clients.Mioweave.URL, "token_file": tokenFile, "token": "new-private-bridge-token"})
	w = panelRequest(t, h, http.MethodPost, "/api/panel/mioweave", string(body), nil)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "new-private-bridge-token") {
		t.Fatalf("update failed or leaked: %d %s", w.Code, w.Body.String())
	}
	token, err = readPanelToken(tokenFile)
	if err != nil || token != "new-private-bridge-token" {
		t.Fatal("token replacement was not applied")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(tokenFile)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatal("updated credential file must be private")
		}
	}
	// An empty replacement preserves the existing secret rather than erasing it.
	body, _ = json.Marshal(map[string]string{"url": s.cfg.Clients.Mioweave.URL, "token_file": tokenFile})
	w = panelRequest(t, h, http.MethodPost, "/api/panel/mioweave", string(body), nil)
	token, err = readPanelToken(tokenFile)
	if w.Code != http.StatusOK || err != nil || token != "new-private-bridge-token" {
		t.Fatal("settings-only save erased a credential")
	}
}

func TestPanelRejectsTokenSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges")
	}
	for _, directory := range []bool{false, true} {
		t.Run(fmt.Sprintf("directory=%v", directory), func(t *testing.T) {
			s, path, tokenFile := panelTestServer(t)
			originalConfig, _ := os.ReadFile(path)
			var outside string
			if directory {
				outside = filepath.Join(t.TempDir(), "relocated")
				if err := os.Rename(filepath.Dir(tokenFile), outside); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, filepath.Dir(tokenFile)); err != nil {
					t.Fatal(err)
				}
				outside = filepath.Join(outside, "mioweave-token")
			} else {
				outside = filepath.Join(t.TempDir(), "external-token")
				if err := os.WriteFile(outside, []byte("private-bridge-token\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(tokenFile); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, tokenFile); err != nil {
					t.Fatal(err)
				}
			}
			body, _ := json.Marshal(map[string]string{"url": s.cfg.Clients.Mioweave.URL, "token_file": tokenFile, "token": "replacement-token"})
			w := panelRequest(t, s.Handler(), http.MethodPost, "/api/panel/mioweave", string(body), nil)
			if w.Code != http.StatusConflict {
				t.Fatalf("symlink overwrite allowed: %d %s", w.Code, w.Body.String())
			}
			data, _ := os.ReadFile(path)
			if !bytes.Equal(data, originalConfig) {
				t.Fatal("rejected symlink modified configuration")
			}
			token, _ := readPanelToken(outside)
			if token != "private-bridge-token" {
				t.Fatal("symlink target modified")
			}
		})
	}
}

func TestPanelTunnelUnixReadinessTransitions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix socket diagnostics")
	}
	socket := filepath.Join(t.TempDir(), "admin.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	var ready atomic.Bool
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.WriteHeader(http.StatusOK)
		case "/readyz":
			if !ready.Load() {
				w.WriteHeader(http.StatusServiceUnavailable)
			}
		case "/api/status":
			_, _ = fmt.Fprint(w, `{"version":"test","api_key":"external-secret","channels":[{"name":"main","enabled":true,"transport_kind":"http-streamable","token":"external-secret"}]}`)
		default:
			http.NotFound(w, r)
		}
	})}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	out := probePanelTunnel(context.Background(), socket, "openai")
	if !out.Connected || out.Ready {
		t.Fatalf("running but unready misclassified: %+v", out)
	}
	ready.Store(true)
	out = probePanelTunnel(context.Background(), socket, "openai")
	if !out.Connected || !out.Ready {
		t.Fatalf("ready transition lost: %+v", out)
	}
	data, _ := json.Marshal(out)
	if strings.Contains(string(data), "external-secret") || strings.Contains(string(data), "api_key") {
		t.Fatal("raw external diagnostics leaked credential fields")
	}
}

func TestPanelPrivateCredentialPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission semantics")
	}
	s, _, tokenFile := panelTestServer(t)
	if err := os.Chmod(tokenFile, 0o644); err != nil {
		t.Fatal(err)
	}
	w := panelRequest(t, s.Handler(), http.MethodPost, "/api/panel/mioweave/token", `{}`, nil)
	if w.Code != http.StatusConflict || strings.Contains(w.Body.String(), "private-bridge-token") {
		t.Fatal("public credential file was revealed")
	}
	if err := os.Chmod(filepath.Dir(tokenFile), 0o755); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"url": s.cfg.Clients.Mioweave.URL, "token_file": tokenFile, "token": "replacement-token"})
	w = panelRequest(t, s.Handler(), http.MethodPost, "/api/panel/mioweave", string(body), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("private credential replacement: %d %s", w.Code, w.Body.String())
	}
	file, _ := os.Stat(tokenFile)
	dir, _ := os.Stat(filepath.Dir(tokenFile))
	if file.Mode().Perm() != 0o600 || dir.Mode().Perm() != 0o700 {
		t.Fatal("credential permissions were not repaired")
	}
}
