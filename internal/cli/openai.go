package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Chillizu/miodesk/internal/config"
	"github.com/Chillizu/miodesk/internal/server"
	"github.com/Chillizu/miodesk/internal/service"
	"github.com/Chillizu/miodesk/internal/workspace"
	"github.com/Chillizu/miodesk/internal/xdg"
)

const defaultRuntimeKeyName = "openai-runtime-key"

var profileNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

type openAISettings struct {
	ClientPath string
	Profile    string
	ProfileDir string
	KeyFile    string
	TunnelID   string
}

func defaultRuntimeKeyFile() (string, error) {
	dir, err := xdg.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, defaultRuntimeKeyName), nil
}

func defaultTunnelClientProfileDir() (string, error) {
	dir, err := xdg.ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(dir), "tunnel-client"), nil
}

func openAISettingsFromConfig(cfg *config.Config, requireCredentials bool) (openAISettings, error) {
	openai := cfg.Tunnel.OpenAI
	profile := openai.Profile
	if profile == "" {
		profile = config.DefaultOpenAIProfile
	}
	if !profileNamePattern.MatchString(profile) {
		return openAISettings{}, fmt.Errorf("tunnel.openai.profile %q is not a valid profile name", profile)
	}

	profileDir := openai.ProfileDir
	if profileDir == "" {
		var err error
		profileDir, err = defaultTunnelClientProfileDir()
		if err != nil {
			return openAISettings{}, err
		}
	}
	profileDir, err := filepath.Abs(profileDir)
	if err != nil {
		return openAISettings{}, fmt.Errorf("resolve tunnel profile directory: %w", err)
	}

	keyFile := openai.RuntimeKeyFile
	if keyFile == "" {
		keyFile, err = defaultRuntimeKeyFile()
		if err != nil {
			return openAISettings{}, err
		}
	}
	keyFile, err = filepath.Abs(keyFile)
	if err != nil {
		return openAISettings{}, fmt.Errorf("resolve runtime key file: %w", err)
	}

	settings := openAISettings{
		Profile:    profile,
		ProfileDir: profileDir,
		KeyFile:    keyFile,
		TunnelID:   strings.TrimSpace(openai.TunnelID),
	}
	if !requireCredentials {
		return settings, nil
	}
	if settings.TunnelID == "" {
		return openAISettings{}, fmt.Errorf("OpenAI tunnel is not configured: missing tunnel id")
	}
	if !strings.HasPrefix(settings.TunnelID, "tunnel_") {
		return openAISettings{}, fmt.Errorf("OpenAI tunnel id %q must start with tunnel_", settings.TunnelID)
	}
	if err := validateRuntimeKeyFile(settings.KeyFile); err != nil {
		return openAISettings{}, err
	}
	clientPath := openai.ClientPath
	if clientPath == "" {
		clientPath, err = exec.LookPath("tunnel-client")
		if err != nil {
			return openAISettings{}, fmt.Errorf("tunnel-client not found on PATH")
		}
	} else {
		clientPath, err = exec.LookPath(clientPath)
		if err != nil {
			return openAISettings{}, fmt.Errorf("configured tunnel-client %q is not executable: %w", openai.ClientPath, err)
		}
	}
	settings.ClientPath = clientPath
	return settings, nil
}

func validateRuntimeKeyFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("OpenAI runtime key file does not exist: %s", path)
		}
		return fmt.Errorf("inspect OpenAI runtime key file %s: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("OpenAI runtime key path is a directory: %s", path)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("OpenAI runtime key path is not a regular file: %s", path)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("OpenAI runtime key file %s is group/world accessible; chmod 600 it first", path)
	}
	return nil
}

func openAIProfilePath(settings openAISettings) string {
	return filepath.Join(settings.ProfileDir, settings.Profile+".yaml")
}

// ensureOpenAIProfile delegates YAML ownership to tunnel-client. This keeps
// the external profile format out of miodesk while ensuring the local MCP URL
// and secret reference are always the values selected by the user.
func ensureOpenAIProfile(cfg *config.Config, mcpURL string, force bool) (openAISettings, error) {
	settings, err := openAISettingsFromConfig(cfg, true)
	if err != nil {
		return openAISettings{}, err
	}
	if !force {
		if _, err := os.Stat(openAIProfilePath(settings)); err == nil {
			return settings, nil
		} else if !os.IsNotExist(err) {
			return openAISettings{}, fmt.Errorf("inspect tunnel-client profile: %w", err)
		}
	}
	if err := os.MkdirAll(settings.ProfileDir, 0o700); err != nil {
		return openAISettings{}, fmt.Errorf("create tunnel-client profile directory: %w", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(settings.ProfileDir, 0o700); err != nil {
			return openAISettings{}, fmt.Errorf("secure tunnel-client profile directory: %w", err)
		}
	}

	args := []string{
		"init",
		"--sample", "sample_mcp_remote_no_auth",
		"--profile", settings.Profile,
		"--profile-dir", settings.ProfileDir,
		"--tunnel-id", settings.TunnelID,
		"--mcp-server-url", mcpURL,
		"--control-plane-api-key-ref", "file:" + settings.KeyFile,
	}
	if force {
		args = append(args, "--force")
	}
	cmd := exec.Command(settings.ClientPath, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		// Do not include tunnel-client output: a future client must not be able
		// to accidentally echo a credential into the miodesk terminal/log.
		_ = output
		return openAISettings{}, fmt.Errorf("tunnel-client profile setup failed: %w", err)
	}
	if _, err := os.Stat(openAIProfilePath(settings)); err != nil {
		return openAISettings{}, fmt.Errorf("tunnel-client did not create profile %s: %w", openAIProfilePath(settings), err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(openAIProfilePath(settings), 0o600); err != nil {
			return openAISettings{}, fmt.Errorf("secure tunnel-client profile: %w", err)
		}
	}
	return settings, nil
}

func openAILocalMCPURL(port int) string {
	return "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) + "/mcp"
}

type setupChoices struct {
	Workspace       string
	Port            int
	ConfigureOpenAI bool
	TunnelID        string
	RuntimeKeyFile  string
	Force           bool
}

// interactiveSetup is deliberately a thin prompt layer over the same setup
// path used by flags. It never installs software or starts a process; it only
// collects values before the existing validation and save logic runs.
func interactiveSetup(in io.Reader, out io.Writer, cfg *config.Config, initialWorkspace string, existing bool) (setupChoices, error) {
	reader := bufio.NewReader(in)
	if initialWorkspace == "" {
		initialWorkspace = cfg.Workspace.Root
	}
	workspaceRoot, err := promptText(reader, out, "Workspace root", initialWorkspace)
	if err != nil {
		return setupChoices{}, err
	}

	port, err := promptPort(reader, out, "Local MCP port", cfg.Server.Port)
	if err != nil {
		return setupChoices{}, err
	}

	configured := cfg.Tunnel.Provider == "openai" && cfg.Tunnel.OpenAI.TunnelID != ""
	configureOpenAI, err := promptYesNo(reader, out, "Configure OpenAI Secure MCP Tunnel now", configured)
	if err != nil {
		return setupChoices{}, err
	}

	choices := setupChoices{
		Workspace:       workspaceRoot,
		Port:            port,
		ConfigureOpenAI: configureOpenAI,
		TunnelID:        cfg.Tunnel.OpenAI.TunnelID,
		RuntimeKeyFile:  cfg.Tunnel.OpenAI.RuntimeKeyFile,
		Force:           false,
	}
	if configureOpenAI {
		choices.TunnelID, err = promptText(reader, out, "OpenAI tunnel id", choices.TunnelID)
		if err != nil {
			return setupChoices{}, err
		}
		if choices.RuntimeKeyFile == "" {
			choices.RuntimeKeyFile, err = defaultRuntimeKeyFile()
			if err != nil {
				return setupChoices{}, err
			}
		}
		choices.RuntimeKeyFile, err = promptText(reader, out, "Runtime API key file", choices.RuntimeKeyFile)
		if err != nil {
			return setupChoices{}, err
		}
	}

	if existing && filepath.Clean(cfg.Workspace.Root) != filepath.Clean(choices.Workspace) {
		choices.Force, err = promptYesNo(reader, out, "Change the existing workspace root", false)
		if err != nil {
			return setupChoices{}, err
		}
	}
	return choices, nil
}

func promptText(in *bufio.Reader, out io.Writer, label, defaultValue string) (string, error) {
	displayDefault := defaultValue
	if displayDefault == "" {
		displayDefault = "none"
	}
	fmt.Fprintf(out, "%s [%s]: ", label, displayDefault)
	line, err := in.ReadString('\n')
	line = strings.TrimSpace(line)
	if err != nil && len(line) == 0 {
		if errors.Is(err, io.EOF) {
			return defaultValue, nil
		}
		return "", err
	}
	if line == "" {
		return defaultValue, nil
	}
	return line, nil
}

func promptPort(in *bufio.Reader, out io.Writer, label string, defaultValue int) (int, error) {
	for {
		value, err := promptText(in, out, label, strconv.Itoa(defaultValue))
		if err != nil {
			return 0, err
		}
		port, err := strconv.Atoi(value)
		if err == nil && port >= 0 && port <= 65535 {
			return port, nil
		}
		fmt.Fprintln(out, "Please enter a port from 0 to 65535.")
	}
}

func promptYesNo(in *bufio.Reader, out io.Writer, label string, defaultValue bool) (bool, error) {
	choice := "y/N"
	if defaultValue {
		choice = "Y/n"
	}
	for {
		fmt.Fprintf(out, "%s [%s]: ", label, choice)
		line, err := in.ReadString('\n')
		line = strings.ToLower(strings.TrimSpace(line))
		if err != nil && len(line) == 0 {
			if errors.Is(err, io.EOF) {
				return defaultValue, nil
			}
			return false, err
		}
		switch line {
		case "":
			return defaultValue, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		default:
			fmt.Fprintln(out, "Please answer y or n.")
		}
	}
}

func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func runSetup(args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("setup", stderr)
	workspaceFlag := fs.String("workspace", "", "workspace root (default: current directory on first setup)")
	portFlag := fs.Int("port", config.DefaultPort, "fixed local MCP port (use 0 only for local ephemeral tests)")
	tunnelIDFlag := fs.String("tunnel-id", "", "OpenAI Secure MCP Tunnel id")
	keyFlag := fs.String("runtime-key-file", "", "path to the OpenAI runtime API key file")
	profileFlag := fs.String("profile", "", "tunnel-client profile name (default: miodesk)")
	profileDirFlag := fs.String("profile-dir", "", "tunnel-client profile directory")
	clientFlag := fs.String("tunnel-client", "", "path or command name for tunnel-client")
	force := fs.Bool("force", false, "allow changing an existing workspace root")
	interactiveFlag := fs.Bool("interactive", false, "prompt for setup values")
	if help, err := parseFlags(fs, args); help {
		return 0
	} else if err != nil {
		hintf(stderr, "run `miodesk setup -h`")
		return 2
	}
	if !requireNoPositional(fs, stderr) {
		return 2
	}
	if *interactiveFlag && len(args) != 1 {
		errf(stderr, "--interactive cannot be combined with other setup arguments")
		return 2
	}
	interactive := *interactiveFlag || (len(args) == 0 && stdinIsTerminal())
	if interactive && !stdinIsTerminal() {
		errf(stderr, "interactive setup requires a terminal")
		hintf(stderr, "use explicit flags for scripts, or run `miodesk setup` in a terminal")
		return 2
	}
	portSet := flagWasSet(fs, "port")
	workspaceValue := *workspaceFlag
	portValue := *portFlag
	tunnelIDValue := *tunnelIDFlag
	keyValue := *keyFlag
	forceValue := *force
	configureOpenAI := false

	path, err := config.Path()
	if err != nil {
		errf(stderr, "%v", err)
		return 1
	}
	cfg, existed, err := config.LoadOrDefault(path)
	if err != nil {
		errf(stderr, "%v", err)
		hintf(stderr, "fix %s, or delete it and run `miodesk setup`", path)
		return 1
	}
	if interactive {
		initialWorkspace := cfg.Workspace.Root
		if !existed {
			initialWorkspace, err = os.Getwd()
			if err != nil {
				errf(stderr, "resolve current directory: %v", err)
				return 1
			}
		}
		choices, promptErr := interactiveSetup(os.Stdin, stdout, cfg, initialWorkspace, existed)
		if promptErr != nil {
			errf(stderr, "interactive setup: %v", promptErr)
			return 1
		}
		workspaceValue = choices.Workspace
		portValue = choices.Port
		portSet = true
		configureOpenAI = choices.ConfigureOpenAI
		if configureOpenAI {
			tunnelIDValue = choices.TunnelID
			keyValue = choices.RuntimeKeyFile
		}
		forceValue = forceValue || choices.Force
	}

	if !existed {
		root := workspaceValue
		if root == "" {
			root, err = os.Getwd()
			if err != nil {
				errf(stderr, "resolve current directory: %v", err)
				return 1
			}
		}
		cfg.Workspace.Root = root
	} else if workspaceValue != "" && filepath.Clean(cfg.Workspace.Root) != filepath.Clean(workspaceValue) {
		if !forceValue {
			warnf(stdout, "configuration already exists: %s", path)
			hintf(stdout, "re-run with --force to change the workspace root")
			return 1
		}
		cfg.Workspace.Root = workspaceValue
	}
	if portSet {
		cfg.Server.Port = portValue
	}
	if *tunnelIDFlag != "" || configureOpenAI {
		cfg.Tunnel.Provider = "openai"
		cfg.Tunnel.OpenAI.TunnelID = strings.TrimSpace(tunnelIDValue)
		// The OpenAI tunnel is the remote boundary. Drop credentials from an
		// older custom/token mode so connect cannot accidentally retain a
		// stale public bearer configuration.
		cfg.Remote.Mode = ""
		cfg.Remote.Token = ""
	}
	if keyValue != "" {
		cfg.Tunnel.OpenAI.RuntimeKeyFile = keyValue
	}
	if *profileFlag != "" {
		cfg.Tunnel.OpenAI.Profile = *profileFlag
	}
	if *profileDirFlag != "" {
		cfg.Tunnel.OpenAI.ProfileDir = *profileDirFlag
	}
	if *clientFlag != "" {
		cfg.Tunnel.OpenAI.ClientPath = *clientFlag
	}
	if err := cfg.Validate(); err != nil {
		errf(stderr, "%v", err)
		return 1
	}
	ws, err := workspace.New(cfg.Workspace.Root)
	if err != nil {
		errf(stderr, "workspace: %v", err)
		hintf(stderr, "create the directory or pass --workspace with an existing directory")
		return 1
	}
	// Persist the canonical absolute root. The service and future invocations
	// may have a different working directory, so a relative root would be
	// surprising on a newly configured device.
	cfg.Workspace.Root = ws.Root()

	settings, err := openAISettingsFromConfig(cfg, false)
	if err != nil {
		errf(stderr, "%v", err)
		return 1
	}
	openaiConfigured := cfg.Tunnel.Provider == "openai" && cfg.Tunnel.OpenAI.TunnelID != ""
	if openaiConfigured {
		if cfg.Server.Port == 0 {
			errf(stderr, "OpenAI tunnel setup needs a fixed local port; choose --port 8787 or another unused port")
			return 1
		}
		settings, err = openAISettingsFromConfig(cfg, true)
		if err != nil {
			errf(stderr, "%v", err)
			hintf(stderr, "create the runtime key file first, then rerun `miodesk setup`")
			return 1
		}
		// Persist the non-secret default path so future `connect` calls do not
		// depend on the directory from which setup was invoked.
		// Store absolute paths so a user service is independent of its working
		// directory. These are paths only; the runtime key contents stay outside
		// config.toml.
		cfg.Tunnel.OpenAI.RuntimeKeyFile = settings.KeyFile
		cfg.Tunnel.OpenAI.ProfileDir = settings.ProfileDir
	}
	if err := cfg.Save(path); err != nil {
		errf(stderr, "write config: %v", err)
		return 1
	}

	okf(stdout, "configuration ready: %s", path)
	okf(stdout, "workspace: %s", ws.Root())
	okf(stdout, "local MCP endpoint: %s", openAILocalMCPURL(cfg.Server.Port))
	if cfg.Tunnel.Provider == "openai" {
		if openaiConfigured {
			if _, err := ensureOpenAIProfile(cfg, openAILocalMCPURL(cfg.Server.Port), true); err != nil {
				errf(stderr, "%v", err)
				return 1
			}
			okf(stdout, "OpenAI tunnel-client profile: %s", openAIProfilePath(settings))
		} else {
			infof(stdout, "OpenAI Secure MCP Tunnel is the default, but no tunnel id is configured yet")
			hintf(stdout, "run `miodesk setup --tunnel-id tunnel_… --runtime-key-file %s` after creating the tunnel and key", settings.KeyFile)
		}
	} else {
		infof(stdout, "existing connection mode preserved: %s", cfg.Tunnel.Provider)
	}

	fmt.Fprintln(stdout, "\nNext:")
	if service.Supported() {
		fmt.Fprintln(stdout, "  miodesk service install")
		fmt.Fprintln(stdout, "  miodesk service start")
	} else if cfg.Tunnel.Provider == "openai" && openaiConfigured {
		fmt.Fprintln(stdout, "  miodesk connect")
	} else {
		fmt.Fprintln(stdout, "  miodesk serve")
	}
	if cfg.Tunnel.Provider == "openai" && openaiConfigured {
		fmt.Fprintln(stdout, "  tunnel-client doctor --profile "+cfg.Tunnel.OpenAI.Profile+" --explain")
		if service.Supported() {
			fmt.Fprintln(stdout, "  miodesk service status")
		}
	} else {
		fmt.Fprintln(stdout, "  miodesk doctor")
	}
	return 0
}

func runOpenAIConnect(cfg *config.Config, ws *workspace.Workspace, stdout, stderr io.Writer, refreshProfile bool) int {
	if cfg.Server.Port == 0 {
		errf(stderr, "OpenAI tunnel requires a fixed local port")
		hintf(stderr, "run `miodesk setup --port 8787`, or choose another fixed --port")
		return 1
	}
	if cfg.Remote.Mode != "" && cfg.Remote.Mode != "local" {
		errf(stderr, "OpenAI tunnel expects the MCP server to stay local, but remote.mode is %q", cfg.Remote.Mode)
		hintf(stderr, "set remote.mode = \"local\"; the tunnel itself supplies the remote access boundary")
		return 1
	}
	settings, err := openAISettingsFromConfig(cfg, true)
	if err != nil {
		errf(stderr, "%v", err)
		hintf(stderr, "run `miodesk setup --tunnel-id tunnel_… --runtime-key-file <file>`")
		return 1
	}

	ctx, stop := signalContext()
	defer stop()
	if service.Installed() && service.RunningQuick() && runningServerMatchesPort(cfg.Server.Port) {
		if _, err := ensureOpenAIProfile(cfg, openAILocalMCPURL(cfg.Server.Port), refreshProfile); err != nil {
			errf(stderr, "%v", err)
			return 1
		}
		okf(stdout, "using running miodesk service at %s", openAILocalMCPURL(cfg.Server.Port))
		if service.TunnelInstalled() && service.TunnelRunningQuick() {
			okf(stdout, "OpenAI Secure MCP Tunnel service is already running")
			infof(stdout, "inspect it with: miodesk service status")
			return 0
		}
		okf(stdout, "starting OpenAI Secure MCP Tunnel for %s", settings.TunnelID)
		err := waitOpenAITunnel(ctx, settings, stdout, stderr)
		if err != nil && ctx.Err() == nil {
			errf(stderr, "tunnel-client: %v", err)
			return 1
		}
		return 0
	}

	s := server.New(cfg, ws)
	ln, err := s.Listen()
	if err != nil {
		errf(stderr, "%v", err)
		var portErr *server.PortInUseError
		if errors.As(err, &portErr) {
			hintf(stderr, "stop the process using the port, or use `miodesk service start` if the service is installed")
		}
		return 1
	}
	if _, err := ensureOpenAIProfile(cfg, s.MCPURL(), refreshProfile); err != nil {
		_ = ln.Close()
		errf(stderr, "%v", err)
		return 1
	}

	serveDone := make(chan error, 1)
	go func() { serveDone <- s.Serve(ctx, ln) }()
	okf(stdout, "local MCP server: %s", s.MCPURL())
	okf(stdout, "starting OpenAI Secure MCP Tunnel for %s", settings.TunnelID)
	tunnelDone := make(chan error, 1)
	go func() { tunnelDone <- waitOpenAITunnel(ctx, settings, stdout, stderr) }()
	select {
	case err = <-tunnelDone:
		stop()
		serveErr := <-serveDone
		if err != nil && ctx.Err() == nil {
			errf(stderr, "tunnel-client: %v", err)
			return 1
		}
		if serveErr != nil && !errors.Is(serveErr, context.Canceled) {
			errf(stderr, "server: %v", serveErr)
			return 1
		}
	case serveErr := <-serveDone:
		stop()
		<-tunnelDone
		if serveErr != nil && !errors.Is(serveErr, context.Canceled) {
			errf(stderr, "server: %v", serveErr)
			return 1
		}
	}
	okf(stdout, "stopped")
	return 0
}

func runningServerMatchesPort(port int) bool {
	path, err := xdg.StateDir()
	if err != nil {
		return false
	}
	data, err := os.ReadFile(filepath.Join(path, "server.json"))
	if err != nil {
		return false
	}
	var state serverState
	if err := json.Unmarshal(data, &state); err != nil {
		return false
	}
	return state.PID > 0 && state.Port == port && state.StartedAt != "" && serverHealth(state.URL)
}

func waitOpenAITunnel(ctx context.Context, settings openAISettings, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, settings.ClientPath, "run", "--profile-dir", settings.ProfileDir, "--profile", settings.Profile)
	configureProcess(cmd)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = 3 * time.Second
	err := cmd.Run()
	if ctx.Err() != nil {
		return context.Canceled
	}
	return err
}

func signalContext() (context.Context, context.CancelFunc) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	return ctx, stop
}
