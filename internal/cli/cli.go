// Package cli implements the miodesk command-line interface: fast, quiet,
// predictable, with explicit next steps in both output and exit codes.
package cli

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

	"miodesk/internal/config"
)

const usage = `miodesk — a local AI / MCP tool bridge

Usage:
  miodesk <command> [flags]

Commands:
  init       create the default configuration
  serve      run the local MCP server (HTTP + widget)
  connect    run the server and expose it via a tunnel
  tunnel     inspect tunnel providers (list, doctor)
  status     show whether the local server is running
  doctor     check config, workspace, ports, providers
  service    manage the systemd user service             (Linux)
  config     print the config file path
  workspace  print the configured workspace root
  logs       show server logs (systemd journal)
  update     check a release feed and replace the binary
  version    print build information

Run ` + "`miodesk <command> -h`" + ` for command flags.
Set MIODESK_LOG=debug for verbose logging on stderr.
`

// Run executes one command and returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if os.Getenv("MIODESK_LOG") == "debug" {
		slog.SetDefault(slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	}

	cmd := "help"
	if len(args) > 0 {
		cmd = args[0]
	}
	switch cmd {
	case "version":
		return runVersion(stdout)
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
		return runConfigPath(stdout, stderr)
	case "workspace":
		return runWorkspace(stdout, stderr)
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
	fs.Usage = func() {} // parse failures get our own hint, not a flag dump
	return fs
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
