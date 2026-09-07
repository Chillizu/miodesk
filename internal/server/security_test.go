package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewAuthorize(t *testing.T) {
	if a, err := NewAuthorize("", ""); err != nil || a.Mode() != AccessLocal {
		t.Fatal("empty mode should be local")
	}
	if _, err := NewAuthorize("token", ""); err == nil {
		t.Fatal("token mode without token must error")
	}
	if _, err := NewAuthorize("banana", "x"); err == nil {
		t.Fatal("unknown mode must error")
	}
	if a, err := NewAuthorize("unsafe", ""); err != nil || a.Mode() != AccessUnsafe {
		t.Fatal("unsafe mode should be accepted")
	}
}

// NewAuthorizeMust is a test helper: panics on configuration error.
func NewAuthorizeMust(mode, token string) *Authorize {
	a, err := NewAuthorize(mode, token)
	if err != nil {
		panic(err)
	}
	return a
}

func TestMiddlewareLocalMode(t *testing.T) {
	a := NewAuthorizeMust("local", "")
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	h := a.Middleware(next)

	// No Origin (curl / SDK clients) passes.
	req := httptest.NewRequest("POST", "/mcp", nil)
	if code := serve(h, req); code != 200 {
		t.Errorf("no-origin request = %d, want 200", code)
	}
	// Same-origin browser request passes.
	req = httptest.NewRequest("POST", "/mcp", nil)
	req.Host = "127.0.0.1:8791"
	req.Header.Set("Origin", "http://127.0.0.1:8791")
	if code := serve(h, req); code != 200 {
		t.Errorf("same-origin = %d, want 200", code)
	}
	// Cross-origin (DNS rebinding) is rejected with 403.
	req = httptest.NewRequest("POST", "/mcp", nil)
	req.Host = "127.0.0.1:8791"
	req.Header.Set("Origin", "https://evil.example")
	if code := serve(h, req); code != 403 {
		t.Errorf("cross-origin = %d, want 403", code)
	}
	// /healthz is exempt (service probes).
	req = httptest.NewRequest("GET", "/healthz", nil)
	req.Header.Set("Origin", "https://evil.example")
	if code := serve(h, req); code != 200 {
		t.Errorf("healthz = %d, want 200", code)
	}
}

func TestMiddlewareTokenMode(t *testing.T) {
	a := NewAuthorizeMust("token", "s3cret-token")
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	h := a.Middleware(next)

	// Missing token → 401 with a WWW-Authenticate challenge.
	req := httptest.NewRequest("POST", "/mcp", nil)
	resp := record(h, req)
	if resp.code != 401 || !strings.Contains(resp.header.Get("WWW-Authenticate"), "Bearer") {
		t.Errorf("no token = %d %q", resp.code, resp.header.Get("WWW-Authenticate"))
	}
	// Wrong token → 401.
	req = httptest.NewRequest("POST", "/mcp", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	if code := serve(h, req); code != 401 {
		t.Errorf("wrong token = %d, want 401", code)
	}
	// Correct token → 200.
	req = httptest.NewRequest("POST", "/mcp", nil)
	req.Header.Set("Authorization", "Bearer s3cret-token")
	if code := serve(h, req); code != 200 {
		t.Errorf("good token = %d, want 200", code)
	}
	// A hostile cross-origin request with the token still passes (auth wins).
	req = httptest.NewRequest("POST", "/mcp", nil)
	req.Host = "127.0.0.1:8791"
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Authorization", "Bearer s3cret-token")
	if code := serve(h, req); code != 200 {
		t.Errorf("cross-origin + token = %d, want 200", code)
	}
	// /healthz stays open.
	req = httptest.NewRequest("GET", "/healthz", nil)
	if code := serve(h, req); code != 200 {
		t.Errorf("healthz = %d, want 200", code)
	}
}

func TestMiddlewareUnsafeMode(t *testing.T) {
	a := NewAuthorizeMust("unsafe", "")
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	h := a.Middleware(next)
	req := httptest.NewRequest("POST", "/mcp", nil)
	req.Host = "127.0.0.1:8791"
	req.Header.Set("Origin", "https://evil.example")
	if code := serve(h, req); code != 200 {
		t.Errorf("unsafe mode should perform no checks, got %d", code)
	}
}

// TestTokenNeverLeaksInStatus runs a real token-mode server and asserts the
// token does not appear in any data-bearing surface.
func TestTokenNeverLeaksInStatus(t *testing.T) {
	s := newTestServer(t)
	s.cfg.Remote.Mode = "token"
	s.cfg.Remote.Token = "super-secret-token-value"
	auth, err := NewAuthorize(s.cfg.Remote.Mode, s.cfg.Remote.Token)
	if err != nil {
		t.Fatal(err)
	}
	s.auth = auth

	ts := httptest.NewServer(s.Handler())
	defer ts.Close()

	get := func(path string) (int, string) {
		req, _ := http.NewRequest("GET", ts.URL+path, nil)
		req.Header.Set("Authorization", "Bearer super-secret-token-value")
		resp, err := ts.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var sb strings.Builder
		var buf [4096]byte
		for {
			n, err := resp.Body.Read(buf[:])
			sb.Write(buf[:n])
			if err != nil {
				break
			}
		}
		return resp.StatusCode, sb.String()
	}

	if code, body := get("/api/status"); code != 200 {
		t.Fatalf("status = %d", code)
	} else if strings.Contains(body, "super-secret-token-value") {
		t.Error("token leaked in /api/status")
	}
	if code, body := get("/api/edits"); code != 200 || strings.Contains(body, "super-secret-token-value") {
		t.Errorf("api/edits = %d (leak: %v)", code, strings.Contains(body, "super-secret-token-value"))
	}
	if code, body := get("/"); code != 200 || strings.Contains(body, "super-secret-token-value") {
		t.Errorf("widget = %d (leak: %v)", code, strings.Contains(body, "super-secret-token-value"))
	}

	// Unauthenticated /api/status is rejected.
	req, _ := http.NewRequest("GET", ts.URL+"/api/status", nil)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("unauthenticated /api/status = %d, want 401", resp.StatusCode)
	}
}

func TestStatusPayloadHasNoTokenField(t *testing.T) {
	s := newTestServer(t)
	data, err := json.Marshal(s.Status())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "token") {
		t.Errorf("status payload mentions token: %s", data)
	}
}

func serve(h http.Handler, req *http.Request) int {
	resp := record(h, req)
	return resp.code
}

type recorded struct {
	code   int
	header http.Header
}

func record(h http.Handler, req *http.Request) recorded {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return recorded{code: w.Code, header: w.Header()}
}
