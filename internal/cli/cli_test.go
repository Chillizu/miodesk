package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"miodesk/internal/config"
)

// isolatedEnv points every XDG directory at per-test temp dirs so tests never
// touch the user's real configuration.
func isolatedEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
}

func run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestVersion(t *testing.T) {
	code, out, _ := run(t, "version")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	for _, want := range []string{"miodesk", "platform: "} {
		if !strings.Contains(out, want) {
			t.Errorf("version output missing %q:\n%s", want, out)
		}
	}
}

func TestInitThenDoctor(t *testing.T) {
	isolatedEnv(t)
	code, out, _ := run(t, "init")
	if code != 0 {
		t.Fatalf("init exit = %d, output:\n%s", code, out)
	}
	if !strings.Contains(out, "[OK] configuration created") {
		t.Errorf("init output:\n%s", out)
	}
	path, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config not created: %v", err)
	}

	code, out, _ = run(t, "doctor")
	if code != 0 {
		t.Fatalf("doctor exit = %d, output:\n%s", code, out)
	}
	for _, want := range []string{"[OK] config", "[OK] workspace", "[OK] mcp", "11 tools registered"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output missing %q:\n%s", want, out)
		}
	}

	// Second init must not clobber anything.
	code, out, _ = run(t, "init")
	if code != 0 || !strings.Contains(out, "already exists") {
		t.Errorf("second init: exit=%d output:\n%s", code, out)
	}
}

func TestInitWorkspaceOverrideRequiresForce(t *testing.T) {
	isolatedEnv(t)
	run(t, "init")
	code, out, _ := run(t, "init", "--workspace", "/somewhere/else")
	if code != 1 || !strings.Contains(out, "--force") {
		t.Errorf("override without force: exit=%d output:\n%s", code, out)
	}
	code, out, _ = run(t, "init", "--workspace", "/somewhere/else", "--force")
	if code != 0 || !strings.Contains(out, "configuration updated") {
		t.Errorf("override with force: exit=%d output:\n%s", code, out)
	}
}

func TestDoctorJSON(t *testing.T) {
	isolatedEnv(t)
	run(t, "init")
	code, out, _ := run(t, "doctor", "--json")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out, `"checks"`) || !strings.Contains(out, `"status": "ok"`) {
		t.Errorf("doctor --json output:\n%s", out)
	}
}

func TestUnknownCommand(t *testing.T) {
	isolatedEnv(t)
	code, _, errOut := run(t, "frobnicate")
	if code != 2 || !strings.Contains(errOut, "unknown command") || !strings.Contains(errOut, "miodesk help") {
		t.Errorf("unknown command: exit=%d stderr:\n%s", code, errOut)
	}
}

func TestUpdateRequiresFeed(t *testing.T) {
	isolatedEnv(t)
	if code, _, errOut := run(t, "update"); code != 1 || !strings.Contains(errOut, "--from") {
		t.Errorf("update without feed: exit=%d stderr:\n%s", code, errOut)
	}
}

func TestServiceArgValidation(t *testing.T) {
	isolatedEnv(t)
	if code, _, errOut := run(t, "service"); code != 2 || !strings.Contains(errOut, "missing verb") {
		t.Errorf("service without verb: exit=%d stderr:\n%s", code, errOut)
	}
	if code, _, errOut := run(t, "service", "fly"); code != 2 || !strings.Contains(errOut, "unknown service verb") {
		t.Errorf("service bad verb: exit=%d stderr:\n%s", code, errOut)
	}
}

func TestConfigAndWorkspacePaths(t *testing.T) {
	isolatedEnv(t)
	run(t, "init")
	code, out, _ := run(t, "config")
	if code != 0 || !strings.HasSuffix(out, "config.toml\n") {
		t.Errorf("config output: %q", out)
	}
	code, out, _ = run(t, "workspace")
	if code != 0 || !strings.HasPrefix(out, "/") {
		t.Errorf("workspace output: %q", out)
	}
}

func TestServeOccupiedPort(t *testing.T) {
	isolatedEnv(t)
	ws := t.TempDir()
	run(t, "init", "--workspace", ws)

	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	port := blocker.Addr().(*net.TCPAddr).Port

	code, out, errOut := run(t, "serve", "--port", strconv.Itoa(port))
	if code != 1 {
		t.Fatalf("serve exit = %d, stdout:\n%s", code, out)
	}
	if !strings.Contains(errOut, "already in use") || !strings.Contains(errOut, "Hint") {
		t.Errorf("serve stderr:\n%s", errOut)
	}
}

func TestStatusWithoutServer(t *testing.T) {
	isolatedEnv(t)
	code, out, _ := run(t, "status")
	if code != 0 || !strings.Contains(out, "no running server found") {
		t.Errorf("status output:\n%s", out)
	}
}

func TestNormalizeJournalTime(t *testing.T) {
	for input, want := range map[string]string{
		"10m":   "10 minutes ago",
		"2h":    "2 hours ago",
		"3d":    "3 days ago",
		"45s":   "45 seconds ago",
		"today": "today",
	} {
		if got := normalizeJournalTime(input); got != want {
			t.Errorf("normalizeJournalTime(%q) = %q, want %q", input, got, want)
		}
	}
}

func buildBinary(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "miodesk")
	cmd := exec.Command("go", "build", "-buildvcs=false", "-o", bin, "miodesk/cmd/miodesk")
	cmd.Dir = root
	cmd.Env = buildEnv(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

// buildEnv gives the `go build` child process the real user environment:
// isolatedEnv pointed HOME and the XDG vars at temp dirs, and the Go toolchain
// would otherwise create a fresh module/build cache there.
func buildEnv(t *testing.T) []string {
	t.Helper()
	u, err := user.Current()
	if err != nil {
		t.Fatalf("resolve user home: %v", err)
	}
	var env []string
	for _, kv := range os.Environ() {
		switch {
		case strings.HasPrefix(kv, "XDG_CONFIG_HOME="),
			strings.HasPrefix(kv, "XDG_CACHE_HOME="),
			strings.HasPrefix(kv, "XDG_DATA_HOME="),
			strings.HasPrefix(kv, "XDG_STATE_HOME="),
			strings.HasPrefix(kv, "HOME="):
			continue
		}
		env = append(env, kv)
	}
	return append(env, "HOME="+u.HomeDir, "GOPATH="+filepath.Join(u.HomeDir, "go"))
}

// TestBinaryServeStdio runs the real binary as a local MCP client would:
// `miodesk serve --stdio`, driven by the official SDK's command transport.
func TestBinaryServeStdio(t *testing.T) {
	isolatedEnv(t)
	ws := t.TempDir()
	if code, out, _ := run(t, "init", "--workspace", ws); code != 0 {
		t.Fatalf("init:\n%s", out)
	}
	hello := filepath.Join(ws, "hello.txt")
	if err := os.WriteFile(hello, []byte("from the binary\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	bin := buildBinary(t)
	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "cli-test", Version: "0"}, nil)
	transport := &mcp.CommandTransport{Command: exec.Command(bin, "serve", "--stdio")}
	sess, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer sess.Close()

	listed, err := sess.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	if len(listed.Tools) != 11 {
		t.Errorf("tools = %d, want 10", len(listed.Tools))
	}

	call, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name:      "read",
		Arguments: map[string]any{"path": "hello.txt"},
	})
	if err != nil {
		t.Fatalf("tools/call: %v", err)
	}
	if call.IsError {
		t.Fatalf("read failed: %v", call.Content)
	}
	data, err := json.Marshal(call.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "from the binary") {
		t.Errorf("structured content = %s", data)
	}
}

// setRemote rewrites the [remote] section of the test config in place.
// The section is the last one init writes, so it is replaced to EOF.
func setRemote(t *testing.T, mode, token string) {
	t.Helper()
	path, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	idx := strings.Index(string(data), "[remote]")
	if idx < 0 {
		t.Fatal("no [remote] section in test config")
	}
	updated := string(data)[:idx] + "[remote]\nmode = \"" + mode + "\"\ntoken = \"" + token + "\"\n"
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
}

func setServerHost(t *testing.T, host string) {
	t.Helper()
	path, err := config.Path()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Server.Host = host
	if err := cfg.Save(path); err != nil {
		t.Fatal(err)
	}
}

func TestServeRefusesNonLocalWithoutAuth(t *testing.T) {
	isolatedEnv(t)
	ws := t.TempDir()
	run(t, "init", "--workspace", ws)
	code, _, errOut := run(t, "serve", "--host", "0.0.0.0")
	if code != 1 || !strings.Contains(errOut, "without authentication") {
		t.Errorf("serve 0.0.0.0 local mode: exit=%d stderr:\n%s", code, errOut)
	}
	// token mode is allowed on a public interface.
	setRemote(t, "token", "test-token-123")
	// Port occupied check happens after auth config passes; use a free port.
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Close()
	code, _, errOut = run(t, "serve", "--host", "0.0.0.0", "--port", strconv.Itoa(blocker.Addr().(*net.TCPAddr).Port))
	if code != 1 || !strings.Contains(errOut, "already in use") {
		t.Errorf("serve 0.0.0.0 token mode should proceed to listen: exit=%d stderr:\n%s", code, errOut)
	}
}

func TestConnectRefusesLocalRemoteMode(t *testing.T) {
	isolatedEnv(t)
	ws := t.TempDir()
	run(t, "init", "--workspace", ws)
	// init's saved config already has [remote] with mode = ''; pin it to an
	// explicit "local" so the refusal path is exercised.
	setRemote(t, "local", "")

	code, _, errOut := run(t, "connect", "--provider", "custom", "--url", "https://demo.example.com", "--port", "0")
	if code != 1 || !strings.Contains(errOut, "refuses to open an unauthenticated remote entrance") {
		t.Errorf("connect with remote.mode=local: exit=%d stderr:\n%s", code, errOut)
	}
}

func TestConnectRefusesNonLocalWithoutAuth(t *testing.T) {
	isolatedEnv(t)
	ws := t.TempDir()
	run(t, "init", "--workspace", ws)
	setServerHost(t, "0.0.0.0")

	code, _, errOut := run(t, "connect", "--provider", "custom", "--url", "https://demo.example.com", "--port", "0")
	if code != 1 || !strings.Contains(errOut, "without authentication") {
		t.Errorf("connect 0.0.0.0 local mode: exit=%d stderr:\n%s", code, errOut)
	}
}

func TestResolveRemoteAuth(t *testing.T) {
	isolatedEnv(t)
	cfg := config.Default()

	// Unset mode → token generated and persisted.
	mode, err := resolveRemoteAuth(cfg, false)
	if err != nil || mode != "token" {
		t.Fatalf("unset mode: %q %v", mode, err)
	}
	if cfg.Remote.Token == "" || len(cfg.Remote.Token) < 24 {
		t.Errorf("generated token too weak: %q", cfg.Remote.Token)
	}
	path, _ := config.Path()
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), cfg.Remote.Token) {
		t.Error("generated token should be persisted to the config")
	}

	// Explicit unsafe wins.
	mode, err = resolveRemoteAuth(config.Default(), true)
	if err != nil || mode != "unsafe" {
		t.Fatalf("unsafe: %q %v", mode, err)
	}
	// Explicit local on remote → refused.
	cfg = config.Default()
	cfg.Remote.Mode = "local"
	if _, err := resolveRemoteAuth(cfg, false); err == nil {
		t.Fatal("remote.mode local must refuse remote entrances")
	}
}

func TestDoctorSecurityDiagnostics(t *testing.T) {
	isolatedEnv(t)
	ws := t.TempDir()
	run(t, "init", "--workspace", ws)

	// token mode: OK, token value never printed.
	setRemote(t, "token", "doctor-secret-9")
	code, out, _ := run(t, "doctor")
	if code != 0 || !strings.Contains(out, "remote authentication enabled") {
		t.Errorf("doctor token mode output:\n%s", out)
	}
	if strings.Contains(out, "doctor-secret-9") {
		t.Error("doctor printed the token")
	}

	// unsafe mode: WARN, not a silent INFO.
	setRemote(t, "unsafe", "")
	code, out, _ = run(t, "doctor")
	if code != 0 || !strings.Contains(out, "[WARN] remote security") {
		t.Errorf("doctor unsafe mode output:\n%s", out)
	}
}
