// Package cli implements the miodesk command-line interface: fast, quiet,
// predictable, with explicit next steps in both output and exit codes.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Chillizu/miodesk/internal/config"
	"github.com/Chillizu/miodesk/internal/logging"
)

const usage = `miodesk — a local AI / MCP tool bridge

Usage:
  miodesk <command> [flags]

Commands:
  setup      configure a new device for local use and OpenAI tunnel access
  init       create the default configuration
  serve      run the local MCP server (HTTP + widget)
  connect    run the server and connect it through the OpenAI tunnel
  tunnel     inspect the default connection (list, doctor)
  status     show whether the local server is running
  doctor     check config, workspace, connection, and ports
  service    manage the systemd user service             (Linux)
  config     print the config file path
  workspace  print the configured workspace root
  logs       show server logs (systemd journal)
  update     check a release feed and replace the binary
  version    print build information

Run ` + "`miodesk <command> -h`" + ` for command flags.
Set MIODESK_LOG=debug for verbose logging, or MIODESK_LOG_FORMAT=json for
machine-readable diagnostic records on stderr.
`

// Run executes one command and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	settings := logging.Settings{Level: "info", Format: "text"}
	if value := os.Getenv("MIODESK_LOG"); value != "" {
		settings.Level = value
	}
	if value := os.Getenv("MIODESK_LOG_FORMAT"); value != "" {
		settings.Format = value
	}
	if err := logging.Configure(settings, stderr); err != nil {
		fmt.Fprintf(stderr, "Warning: %v\n", err)
		_ = logging.Configure(logging.Settings{Level: "info", Format: "text"}, stderr)
	}

	cmd := "help"
	if len(args) > 0 {
		cmd = args[0]
	}
	switch cmd {
	case "version":
		return runVersion(args[1:], stdout, stderr)
	case "setup":
		return runSetup(args[1:], stdout, stderr)
	case "init":
		return runInit(args[1:], stdout, stderr)
	case "serve":
		return runServe(args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(args[1:], stdout, stderr)
	case "status":
		return runStatus(args[1:], stdout, stderr)
	case "service":
		return runService(args[1:], stdout, stderr)
	case "config":
		return runConfigPath(args[1:], stdout, stderr)
	case "workspace":
		return runWorkspace(args[1:], stdout, stderr)
	case "connect":
		return runConnect(args[1:], stdout, stderr)
	case "tunnel":
		return runTunnel(args[1:], stdout, stderr)
	case "update":
		return runUpdate(args[1:], stdout, stderr)
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case "logs":
		return runLogs(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "Error: unknown command %q\nHint: run `miodesk help`\n", cmd)
		return 2
	}
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: miodesk %s [flags]\n\nFlags:\n", name)
		fs.PrintDefaults()
	}
	return fs
}

// parseFlags keeps help useful while keeping malformed invocations concise.
// The standard flag package already writes the detailed help or parse error
// to the configured output; callers add their command-specific next step only
// for real parse failures.
func parseFlags(fs *flag.FlagSet, args []string) (help bool, err error) {
	err = fs.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		return true, nil
	}
	return false, err
}

// requireNoPositional rejects arguments that would otherwise be silently
// ignored by the standard flag package. Every command that has no positional
// grammar calls this after parsing so typos fail closed instead of reporting a
// successful operation the user did not actually request.
func requireNoPositional(fs *flag.FlagSet, stderr io.Writer) bool {
	if fs.NArg() == 0 {
		return true
	}
	errf(stderr, "unexpected positional argument")
	hintf(stderr, "run `miodesk %s -h`", fs.Name())
	return false
}

func flagWasSet(fs *flag.FlagSet, name string) bool {
	set := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

// loadConfig resolves the config path and loads it with defaults. ok=false
// means a readable error has already been printed.
func loadConfig(stderr io.Writer) (cfg *config.Config, path string, ok bool) {
	path, err := config.Path()
	if err != nil {
		errf(stderr, "cannot resolve config path: %v", err)
		return nil, "", false
	}
	cfg, _, err = config.LoadOrDefault(path)
	if err != nil {
		errf(stderr, "%v", err)
		hintf(stderr, "fix %s, or delete it and run `miodesk init`", path)
		return nil, path, false
	}
	return cfg, path, true
}

func okf(w io.Writer, format string, a ...any)   { fmt.Fprintf(w, "[OK] "+format+"\n", a...) }
func warnf(w io.Writer, format string, a ...any) { fmt.Fprintf(w, "[WARN] "+format+"\n", a...) }
func infof(w io.Writer, format string, a ...any) { fmt.Fprintf(w, "[INFO] "+format+"\n", a...) }
func hintf(w io.Writer, format string, a ...any) { fmt.Fprintf(w, "Hint: "+format+"\n", a...) }
func errf(w io.Writer, format string, a ...any)  { fmt.Fprintf(w, "Error: "+format+"\n", a...) }
