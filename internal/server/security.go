package server

import (
	"crypto/subtle"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/Chillizu/miodesk/internal/logging"
)

// AccessMode is miodesk's three-tier trust model. Local trusted usage stays
// zero-config; remote entrances require authentication unless the operator
// explicitly opts into unsafe development access.
type AccessMode string

const (
	// AccessLocal: no token; the server must only be reachable from the local
	// machine (loopback bind or stdio). Origin validation stays active — it is
	// the DNS-rebinding defense the MCP Streamable HTTP spec requires.
	AccessLocal AccessMode = "local"
	// AccessToken: every request (except /healthz) must present the bearer
	// token. Same-origin requests are still allowed (the local widget).
	AccessToken AccessMode = "token"
	// AccessUnsafe: no checks at all. Only reachable via an explicit opt-in
	// (`miodesk connect --unsafe-remote` or remote.mode = "unsafe").
	AccessUnsafe AccessMode = "unsafe"
)

// Authorize guards every HTTP surface.
type Authorize struct {
	mode  AccessMode
	token string
}

// NewAuthorize validates an access configuration. An empty mode means local.
func NewAuthorize(mode, token string) (*Authorize, error) {
	switch AccessMode(mode) {
	case "", AccessLocal:
		return &Authorize{mode: AccessLocal}, nil
	case AccessToken:
		if token == "" {
			return nil, fmt.Errorf("remote.mode \"token\" requires remote.token")
		}
		return &Authorize{mode: AccessToken, token: token}, nil
	case AccessUnsafe:
		return &Authorize{mode: AccessUnsafe}, nil
	default:
		return nil, fmt.Errorf("unknown remote.mode %q", mode)
	}
}

// Mode reports the active access mode.
func (a *Authorize) Mode() AccessMode { return a.mode }

// Middleware wraps next with the active trust level:
//
//   - Origin validation always applies in local and token modes: a browser
//     Origin that does not match the request host is rejected with 403, which
//     is the DNS-rebinding defense the MCP Streamable HTTP spec requires.
//   - Token mode additionally requires `Authorization: Bearer <token>` on
//     everything except /healthz.
//   - Unsafe mode performs no checks at all; that is its documented purpose.
func (a *Authorize) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.mode != AccessUnsafe && r.URL.Path != "/healthz" {
			if origin := r.Header.Get("Origin"); origin != "" && !sameOrigin(r.Host, origin) {
				if !(a.mode == AccessToken && requestHasToken(r, a.token)) {
					logAuthDenied(r, "origin")
					http.Error(w, "forbidden origin", http.StatusForbidden)
					return
				}
			}
			if a.mode == AccessToken && !requestHasToken(r, a.token) {
				logAuthDenied(r, "missing_or_invalid_bearer")
				w.Header().Set("WWW-Authenticate", `Bearer realm="miodesk"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func logAuthDenied(r *http.Request, reason string) {
	slog.Warn("auth_denied",
		"request_id", logging.RequestID(r.Context()),
		"reason", reason,
		"method", r.Method,
		"path", r.URL.Path,
		"remote_addr", r.RemoteAddr,
	)
}

func requestHasToken(r *http.Request, want string) bool {
	const prefix = "Bearer "
	auth := r.Header.Get("Authorization")
	if len(auth) <= len(prefix) || !strings.EqualFold(auth[:len(prefix)], prefix) {
		return false
	}
	got := strings.TrimSpace(auth[len(prefix):])
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// sameOrigin compares the Origin's host with the request Host, ignoring the
// scheme: miodesk is plain HTTP behind tunnels, and the host name is the part
// DNS rebinding attacks forge.
func sameOrigin(host, origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, host)
}
