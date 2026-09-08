package tools

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/Chillizu/miodesk/internal/workspace"
)

const (
	DefaultCommandTimeout = 120 // seconds
	MaxCommandTimeout     = 600

	// maxCommandOutput caps captured stdout/stderr for short commands.
	maxCommandOutput = 256 << 10
)

type CommandInput struct {
	Command string `json:"command" jsonschema:"shell command to run, e.g. go test ./..."`
	CWD     string `json:"cwd,omitempty" jsonschema:"working directory inside the workspace (default: root)"`
	Timeout int    `json:"timeout,omitempty" jsonschema:"seconds before the command is killed (default 120, max 600)"`
}

type CommandOutput struct {
	Kind            string `json:"kind"` // "command"
	Command         string `json:"command"`
	CWD             string `json:"cwd"`
	ExitCode        int    `json:"exit_code"`
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	StdoutTruncated bool   `json:"stdout_truncated"`
	StderrTruncated bool   `json:"stderr_truncated"`
	ElapsedMS       int64  `json:"elapsed_ms"`
	TimedOut        bool   `json:"timed_out"`
}

// Command runs a short development command inside the workspace and returns
// stdout, stderr, the exit code, and elapsed time. Privilege escalation tools
// are refused.
func Command(ctx context.Context, ws *workspace.Workspace, in CommandInput) (*CommandOutput, error) {
	p, err := newProc(ctx, ws, in, maxCommandOutput)
	if err != nil {
		return nil, err
	}
	defer p.cancel()

	start := time.Now()
	if err := p.cmd.Start(); err != nil {
		return nil, fmt.Errorf("command: start: %w", err)
	}
	waitErr := waitWithTimeout(p, DefaultTimeout(in.Timeout))

	timedOut := waitErr == errTimedOut
	if !timedOut && p.ctx.Err() != nil {
		return nil, p.ctx.Err()
	}
	if waitErr != nil && !timedOut {
		var exitErr *exec.ExitError
		if !errors.As(waitErr, &exitErr) {
			return nil, fmt.Errorf("command: %w", waitErr)
		}
	}
	return &CommandOutput{
		Kind:            "command",
		Command:         in.Command,
		CWD:             p.relDir,
		ExitCode:        exitCodeOf(p),
		Stdout:          p.stdout.String(),
		Stderr:          p.stderr.String(),
		StdoutTruncated: p.stdout.Truncated(),
		StderrTruncated: p.stderr.Truncated(),
		ElapsedMS:       time.Since(start).Milliseconds(),
		TimedOut:        timedOut,
	}, nil
}

// DefaultTimeout normalizes the requested timeout in seconds.
func DefaultTimeout(t int) int {
	if t <= 0 {
		return DefaultCommandTimeout
	}
	if t > MaxCommandTimeout {
		return MaxCommandTimeout
	}
	return t
}
