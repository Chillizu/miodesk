package server

import (
	"context"
	"crypto/rand"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Chillizu/miodesk/internal/config"
	"github.com/Chillizu/miodesk/internal/logging"
	brand "github.com/Chillizu/miodesk/plugins/miodesk/assets"
)

//go:embed panel/index.html panel/panel.css panel/panel.js
var panelAssets embed.FS

const panelTokenLimit = 4096

func (s *Server) panelHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /panel", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/panel/", http.StatusSeeOther)
	})
	for path, file := range map[string]string{
		"/panel/": "index.html", "/panel/panel.css": "panel.css", "/panel/panel.js": "panel.js",
	} {
		mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != path {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", mime.TypeByExtension(filepath.Ext(file)))
			data, _ := panelAssets.ReadFile("panel/" + file)
			_, _ = w.Write(data)
		})
	}
	mux.HandleFunc("GET /panel/logo.png", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(brand.Logo)
	})
	mux.HandleFunc("GET /api/panel/status", s.panelStatus)
	mux.HandleFunc("POST /api/panel/mioweave", s.panelSaveMioweave)
	mux.HandleFunc("POST /api/panel/mioweave/token", s.panelRevealToken)
	mux.HandleFunc("GET /api/panel/logs", s.panelLogs)
	return localPanel(mux)
}

// Management is deliberately independent of MCP access mode. A remote bearer
// token or unsafe Core must never disable the panel's local/CSRF boundary.
func localPanel(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		peer, _, err := net.SplitHostPort(r.RemoteAddr)
		ip, ipErr := netip.ParseAddr(peer)
		host, _, hostErr := net.SplitHostPort(r.Host)
		if hostErr != nil {
			host = r.Host
		}
		origin := r.Header.Get("Origin")
		fetchSite := r.Header.Get("Sec-Fetch-Site")
		if err != nil || ipErr != nil || !ip.IsLoopback() || !panelLoopbackHost(host) ||
			(origin != "" && origin != "http://"+r.Host) ||
			(fetchSite != "" && fetchSite != "none" && fetchSite != "same-origin") ||
			r.Header.Get("Forwarded") != "" || r.Header.Get("X-Forwarded-For") != "" ||
			r.Header.Get("X-Forwarded-Host") != "" || r.Header.Get("X-Forwarded-Proto") != "" {
			panelError(w, http.StatusForbidden, "Panel requires a direct loopback connection; open http://127.0.0.1:<port>/panel/ on this computer")
			return
		}
		if r.Method == http.MethodPost {
			mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if origin != "http://"+r.Host || r.Header.Get("X-Miodesk-Panel") != "1" || mediaType != "application/json" {
				panelError(w, http.StatusForbidden, "Panel changes require a same-origin JSON request")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func panelLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback() && ip.Zone() == ""
}

func panelJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func panelError(w http.ResponseWriter, status int, message string) {
	panelJSON(w, status, map[string]string{"error": message})
}

func panelDecode(w http.ResponseWriter, r *http.Request, value any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(value); err != nil {
		panelError(w, http.StatusBadRequest, "Invalid panel request JSON")
		return false
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		panelError(w, http.StatusBadRequest, "Expected one JSON object")
		return false
	}
	return true
}

// Call under panelMu. Config is reloaded so edits in a terminal are not silently
// overwritten, while s.cfg continues to describe the running Core instance.
func (s *Server) panelConfig() (*config.Config, string, error) {
	path, err := config.Path()
	if err != nil {
		return nil, "", err
	}
	cfg, exists, err := config.LoadOrDefault(path)
	if err == nil && !exists {
		copy := *s.cfg
		cfg = &copy
	}
	if err == nil && cfg.Clients.Mioweave.TokenFile == "" {
		cfg.Clients.Mioweave.TokenFile = panelManagedTokenFile(path)
	}
	return cfg, path, err
}

type panelMioweave struct {
	URL             string `json:"url"`
	TokenFile       string `json:"token_file"`
	TokenConfigured bool   `json:"token_configured"`
	TokenSource     string `json:"token_source"`
	Connected       bool   `json:"connected"`
	Authorized      bool   `json:"authorized"`
	Tools           int    `json:"tools"`
	Error           string `json:"error,omitempty"`
}

type panelChannel struct {
	Name          string `json:"name"`
	Enabled       bool   `json:"enabled"`
	TransportKind string `json:"transport_kind"`
	Reason        string `json:"reason,omitempty"`
}

type panelTunnel struct {
	Configured     bool           `json:"configured"`
	Connected      bool           `json:"connected"`
	Ready          bool           `json:"ready"`
	AdminTransport string         `json:"admin_transport"`
	SocketPath     string         `json:"socket_path"`
	Version        string         `json:"version,omitempty"`
	Channels       []panelChannel `json:"channels"`
	Error          string         `json:"error,omitempty"`
	Notice         string         `json:"notice"`
}

func (s *Server) panelStatus(w http.ResponseWriter, r *http.Request) {
	s.panelMu.Lock()
	cfg, path, err := s.panelConfig()
	s.panelMu.Unlock()
	if err != nil {
		panelError(w, http.StatusInternalServerError, "Cannot read miodesk configuration; repair config.toml in a terminal")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	// Independent external clients cannot delay each other's diagnostics.
	bridgeDone := make(chan panelMioweave, 1)
	go func() { bridgeDone <- probeMioweave(ctx, cfg.Clients.Mioweave) }()
	tunnel := probePanelTunnel(ctx, cfg.Panel.TunnelSocket, cfg.Tunnel.Provider)
	bridge := <-bridgeDone
	panelJSON(w, http.StatusOK, struct {
		Core       LocalStatus       `json:"core"`
		Mioweave   panelMioweave     `json:"mioweave"`
		Tunnel     panelTunnel       `json:"tunnel"`
		Logging    map[string]string `json:"logging"`
		ConfigPath string            `json:"config_path"`
	}{s.LocalStatus(), bridge, tunnel, map[string]string{"level": s.cfg.Logging.Level, "format": s.cfg.Logging.Format}, path})
}

func panelBridgeURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		(u.Path != "" && u.Path != "/") || !panelLoopbackHost(u.Hostname()) {
		return nil, errors.New("Bridge URL must be a loopback HTTP address, e.g. http://127.0.0.1:8790 (not the Core MCP address)")
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, errors.New("Bridge port must be between 1 and 65535")
		}
	}
	u.Path = ""
	return u, nil
}

func panelClient(socket string) *http.Client {
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	transport := &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if socket != "" {
				return dialer.DialContext(ctx, "unix", socket)
			}
			host, port, err := net.SplitHostPort(addr)
			if err != nil || !panelLoopbackHost(host) {
				return nil, errors.New("non-loopback connection refused")
			}
			// Never consult DNS or a proxy for optional local diagnostics.
			if strings.EqualFold(host, "localhost") {
				host = "127.0.0.1"
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(host, port))
		},
	}
	return &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func panelGet(ctx context.Context, client *http.Client, address, token string, value any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return 0, err
	}
	if token != "" {
		req.Header.Set("X-WebMCP-Token", token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK && value != nil {
		err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(value)
	}
	return resp.StatusCode, err
}

func probeMioweave(ctx context.Context, settings config.Mioweave) panelMioweave {
	out := panelMioweave{URL: settings.URL, TokenFile: settings.TokenFile, TokenSource: "unknown"}
	u, err := panelBridgeURL(settings.URL)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	token, tokenErr := readPanelToken(settings.TokenFile)
	out.TokenConfigured = tokenErr == nil
	client := panelClient("")
	var health struct {
		OK          bool   `json:"ok"`
		Bridge      string `json:"bridge"`
		TokenSource string `json:"token_source"`
	}
	code, err := panelGet(ctx, client, u.String()+"/health", "", &health)
	if err != nil || code != http.StatusOK || !health.OK || health.Bridge != "mioweave" {
		out.Error = "mioweave Bridge is unavailable; check its service and Bridge URL (default port 8790, not Core port 8787)"
		return out
	}
	out.Connected = true
	if health.TokenSource == "file" || health.TokenSource == "environment" {
		out.TokenSource = health.TokenSource
	}
	if tokenErr != nil {
		out.Error = "Bridge is reachable; select its existing private token file to authorize the connection"
		return out
	}
	var tools struct {
		OK    bool `json:"ok"`
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	code, err = panelGet(ctx, client, u.String()+"/tools", token, &tools)
	if code == http.StatusUnauthorized {
		out.Error = "Bridge rejected the token; Panel and Bridge must reference the same token file"
	} else if err != nil || code != http.StatusOK || !tools.OK {
		out.Error = "Bridge is reachable but tools are unavailable; check its upstream MCP address and Core access mode"
	} else {
		out.Authorized = true
		out.Tools = len(tools.Tools)
	}
	return out
}

func probePanelTunnel(ctx context.Context, socket, provider string) panelTunnel {
	out := panelTunnel{
		Configured: provider != "" && provider != "local", AdminTransport: "unconfigured", SocketPath: socket,
		Channels: []panelChannel{}, Notice: "Tunnel readiness does not prove a live ChatGPT connection; verify a real tool call in ChatGPT",
	}
	if socket == "" {
		return out
	}
	out.AdminTransport = "unix"
	if !filepath.IsAbs(socket) {
		out.Error = "panel.tunnel_socket must be an absolute Unix socket path"
		return out
	}
	client := panelClient(socket)
	code, err := panelGet(ctx, client, "http://localhost/healthz", "", nil)
	if err != nil || code != http.StatusOK {
		out.Error = "Tunnel admin socket is unavailable; check the tunnel service and panel.tunnel_socket"
		return out
	}
	out.Connected = true
	code, err = panelGet(ctx, client, "http://localhost/readyz", "", nil)
	out.Ready = err == nil && code == http.StatusOK
	if !out.Ready {
		out.Error = "Tunnel is running but not ready; inspect Tunnel logs"
	}
	// Whitelist non-secret diagnostics; never proxy raw external config/status.
	var status struct {
		Version  string         `json:"version"`
		Channels []panelChannel `json:"channels"`
	}
	if code, err := panelGet(ctx, client, "http://localhost/api/status", "", &status); err == nil && code == http.StatusOK {
		out.Version = status.Version
		if status.Channels != nil {
			out.Channels = status.Channels
		}
	}
	return out
}

func validPanelToken(token string) bool {
	return token != "" && len(token) <= panelTokenLimit && strings.IndexFunc(token, unicode.IsSpace) < 0 && !strings.ContainsRune(token, 0)
}

func readPanelToken(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("token file must be an absolute path")
	}
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil || !parent.IsDir() {
		return "", errors.New("token directory must not be a symlink")
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return "", errors.New("token directory is unreadable")
	}
	defer root.Close()
	actual, err := root.Stat(".")
	if err != nil || !os.SameFile(parent, actual) {
		return "", errors.New("token directory changed during validation")
	}
	info, err := root.Lstat(filepath.Base(path))
	if err != nil || !info.Mode().IsRegular() || info.Size() > panelTokenLimit+2 {
		return "", errors.New("token file is missing, not a regular file, or too large")
	}
	f, err := root.Open(filepath.Base(path))
	if err != nil {
		return "", errors.New("token file is unreadable")
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) ||
		(runtime.GOOS != "windows" && opened.Mode().Perm()&0o077 != 0) {
		return "", errors.New("token file must be a private regular file")
	}
	data, err := io.ReadAll(io.LimitReader(f, panelTokenLimit+3))
	token := strings.TrimSpace(string(data))
	if err != nil || len(data) > panelTokenLimit+2 || !validPanelToken(token) {
		return "", errors.New("token file must contain a single nonempty token")
	}
	return token, nil
}

func (s *Server) panelRevealToken(w http.ResponseWriter, r *http.Request) {
	var in struct{}
	if !panelDecode(w, r, &in) {
		return
	}
	s.panelMu.Lock()
	defer s.panelMu.Unlock()
	cfg, _, err := s.panelConfig()
	if err != nil {
		panelError(w, http.StatusInternalServerError, "Cannot read miodesk configuration")
		return
	}
	token, err := readPanelToken(cfg.Clients.Mioweave.TokenFile)
	if err != nil {
		panelError(w, http.StatusConflict, "Cannot read the Bridge token; select its existing private token file first")
		return
	}
	panelJSON(w, http.StatusOK, map[string]string{"token": token})
}

func (s *Server) panelSaveMioweave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL       string `json:"url"`
		TokenFile string `json:"token_file"`
		Token     string `json:"token"`
	}
	if !panelDecode(w, r, &in) {
		return
	}
	u, err := panelBridgeURL(strings.TrimSpace(in.URL))
	if err != nil {
		panelError(w, http.StatusBadRequest, err.Error())
		return
	}
	in.Token = strings.TrimSpace(in.Token)
	if in.Token != "" && !validPanelToken(in.Token) {
		panelError(w, http.StatusBadRequest, "Token must be a single value of at most 4096 bytes")
		return
	}
	s.panelMu.Lock()
	defer s.panelMu.Unlock()
	cfg, path, err := s.panelConfig()
	if err != nil {
		panelError(w, http.StatusInternalServerError, "Cannot read miodesk configuration")
		return
	}
	previous := *cfg
	in.TokenFile = strings.TrimSpace(in.TokenFile)
	managed := panelManagedTokenFile(path)
	if in.TokenFile != "" && in.TokenFile != managed && in.TokenFile != previous.Clients.Mioweave.TokenFile {
		panelError(w, http.StatusBadRequest, "Panel credentials must stay in miodesk's private XDG credentials directory")
		return
	}
	if in.Token != "" {
		// A legacy external file can be read, but is never overwritten by Panel.
		in.TokenFile = managed
	} else if in.TokenFile == "" {
		in.TokenFile = previous.Clients.Mioweave.TokenFile
	}
	if in.Token != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		bridge := probeMioweave(ctx, config.Mioweave{URL: u.String(), TokenFile: in.TokenFile})
		cancel()
		if bridge.TokenSource == "environment" {
			panelError(w, http.StatusConflict, "Bridge uses an environment token; restart it with WEBMCP_BRIDGE_TOKEN_FILE before updating the token here")
			return
		}
	}
	cfg.Clients.Mioweave = config.Mioweave{URL: u.String(), TokenFile: in.TokenFile}
	var credentials *os.Root
	var pending string
	if in.Token != "" {
		credentials, pending, err = preparePanelToken(path, in.Token)
		if err != nil {
			panelError(w, http.StatusConflict, "Cannot prepare private miodesk credentials; credential directories and files must not be symlinks")
			return
		}
		defer credentials.Close()
		defer credentials.Remove(pending)
	}
	if err := cfg.Save(path); err != nil {
		panelError(w, http.StatusInternalServerError, "Cannot save miodesk configuration; token was not changed")
		return
	}
	if pending != "" {
		if err := credentials.Rename(pending, "mioweave-token"); err != nil {
			if rollbackErr := previous.Save(path); rollbackErr != nil {
				panelError(w, http.StatusInternalServerError, "Token update failed and configuration rollback failed; repair config.toml in a terminal")
				return
			}
			panelError(w, http.StatusInternalServerError, "Token update failed; previous configuration restored")
			return
		}
	}
	restart := previous.Clients.Mioweave.TokenFile != in.TokenFile
	notice := "Saved. A file-backed Bridge using this same token file reloads token changes immediately; update the extension token after a rotation."
	if restart {
		notice = "Saved. Ensure Bridge uses WEBMCP_BRIDGE_TOKEN_FILE pointing to this file, then restart Bridge once. Panel cannot change an external process's environment."
	}
	panelJSON(w, http.StatusOK, map[string]any{"ok": true, "restart_required": restart, "notice": notice})
}

func panelManagedTokenFile(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), "credentials", "mioweave-token")
}

// Directory-relative operations keep token replacement confined even if a
// directory is renamed during the request. Only this fixed filename is writable.
func preparePanelToken(configPath, token string) (*os.Root, string, error) {
	appDir := filepath.Dir(configPath)
	if err := os.MkdirAll(appDir, 0o700); err != nil {
		return nil, "", err
	}
	appInfo, err := os.Lstat(appDir)
	if err != nil || !appInfo.IsDir() {
		return nil, "", errors.New("configuration directory is not a real directory")
	}
	app, err := os.OpenRoot(appDir)
	if err != nil {
		return nil, "", err
	}
	defer app.Close()
	if err := privatePanelDirectory(app, appInfo); err != nil {
		return nil, "", err
	}
	if err := app.Mkdir("credentials", 0o700); err != nil && !os.IsExist(err) {
		return nil, "", err
	}
	info, err := app.Lstat("credentials")
	if err != nil || !info.IsDir() {
		return nil, "", errors.New("credentials must not be a symlink")
	}
	root, err := app.OpenRoot("credentials")
	if err != nil {
		return nil, "", err
	}
	failed := true
	name := ""
	defer func() {
		if failed {
			if name != "" {
				_ = root.Remove(name)
			}
			_ = root.Close()
		}
	}()
	if err := privatePanelDirectory(root, info); err != nil {
		return nil, "", err
	}
	if info, err := root.Lstat("mioweave-token"); err == nil && !info.Mode().IsRegular() {
		return nil, "", errors.New("credential is not a regular file")
	} else if err != nil && !os.IsNotExist(err) {
		return nil, "", err
	}
	name = ".mioweave-token-" + rand.Text()
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, "", err
	}
	if err = f.Chmod(0o600); err == nil {
		_, err = io.WriteString(f, token+"\n")
	}
	if err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, "", err
	}
	failed = false
	return root, name, nil
}

func privatePanelDirectory(root *os.Root, expected os.FileInfo) error {
	f, err := root.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	actual, err := f.Stat()
	if err != nil || !actual.IsDir() || !os.SameFile(expected, actual) {
		return errors.New("credential directory changed during validation")
	}
	if runtime.GOOS != "windows" {
		return f.Chmod(0o700)
	}
	return nil
}

func (s *Server) panelLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	lines := 100
	if q.Has("lines") {
		var err error
		lines, err = strconv.Atoi(q.Get("lines"))
		if err != nil || lines < 1 || lines > 500 {
			panelError(w, http.StatusBadRequest, "Log lines must be between 1 and 500")
			return
		}
	}
	opts := logging.JournalOptions{Source: q.Get("source"), Level: q.Get("level"), Since: q.Get("since"), Match: q.Get("query"), Lines: lines}
	if opts.Source == "" {
		opts.Source = "all"
	}
	if opts.Level == "" {
		opts.Level = "info"
	}
	if opts.Since == "" {
		opts.Since = "10m"
	}
	validSource := opts.Source == "all" || opts.Source == "core" || opts.Source == "tunnel" || opts.Source == "mioweave"
	validLevel := opts.Level == "debug" || opts.Level == "info" || opts.Level == "warn" || opts.Level == "error"
	if !validSource || !validLevel || len(opts.Match) > 512 || len(opts.Since) > 100 {
		panelError(w, http.StatusBadRequest, "Invalid log source, level, time range, or query")
		return
	}
	s.panelMu.Lock()
	cfg, _, err := s.panelConfig()
	s.panelMu.Unlock()
	if err != nil {
		panelError(w, http.StatusInternalServerError, "Cannot read miodesk configuration")
		return
	}
	secrets := []string{s.cfg.Remote.Token, cfg.Remote.Token}
	if token, err := readPanelToken(cfg.Clients.Mioweave.TokenFile); err == nil {
		secrets = append(secrets, token)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	records := make([]json.RawMessage, 0, lines)
	remaining := 1 << 20
	limitReached := errors.New("panel log size limit")
	err = logging.ReadJournal(ctx, opts, secrets, func(record logging.Record) error {
		data, err := json.Marshal(record)
		if err != nil {
			return err
		}
		if len(data) > remaining {
			return limitReached
		}
		remaining -= len(data)
		records = append(records, data)
		return nil
	})
	notice := ""
	if errors.Is(err, limitReached) {
		notice = "Log response reached the 1 MiB limit; narrow the time range or filter"
	} else if err != nil {
		notice = fmt.Sprintf("Logs unavailable: %v. Check the systemd user journal; foreground servers log to their terminal.", err)
	}
	panelJSON(w, http.StatusOK, struct {
		Records []json.RawMessage `json:"records"`
		Notice  string            `json:"notice,omitempty"`
	}{records, notice})
}
