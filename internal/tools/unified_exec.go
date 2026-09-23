package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/Chillizu/miodesk/internal/workspace"
)

const (
	defaultExecYieldMS     = 10_000
	maxExecYieldMS         = 30_000
	defaultWriteYieldMS    = 1_000
	maxWriteYieldMS        = 30_000
	maxWriteStdinBytes     = 64 << 10
	writeStdinWriteTimeout = 5 * time.Second
)

// ExecCommandInput stays intentionally close to Codex's unified exec surface.
// TTY is opt-in so ordinary build/test commands keep deterministic pipe output.
type ExecCommandInput struct {
	Cmd         string `json:"cmd" jsonschema:"shell command to run"`
	Workdir     string `json:"workdir,omitempty" jsonschema:"working directory inside the workspace (default: root)"`
	TTY         bool   `json:"tty,omitempty" jsonschema:"allocate an interactive terminal; default false"`
	YieldTimeMS int    `json:"yield_time_ms,omitempty" jsonschema:"milliseconds to wait before returning a live session (default 10000, max 30000)"`
	TimeoutMS   int    `json:"timeout_ms,omitempty" jsonschema:"maximum total command runtime in milliseconds (default 120000, max 600000)"`
}

// WriteStdinInput resumes a live exec session. Empty chars is a pure poll.
// On PTY sessions chars are raw terminal input; on pipe sessions Ctrl-C keeps
// the lightweight out-of-band cancellation behavior.
type WriteStdinInput struct {
	SessionID   int    `json:"session_id" jsonschema:"numeric session id returned by exec_command"`
	Chars       string `json:"chars,omitempty" jsonschema:"characters to write; empty string polls; Ctrl-C is raw terminal input for TTY sessions and cancels pipe sessions"`
	YieldTimeMS int    `json:"yield_time_ms,omitempty" jsonschema:"milliseconds to wait for new output or exit (default 1000, max 30000)"`
}

// UnifiedExecOutput mirrors the compact Codex code-mode result shape. session_id
// is present only while the command remains resumable; exit_code is present only
// after the process exits.
type UnifiedExecOutput struct {
	WallTimeSeconds float64 `json:"wall_time_seconds"`
	ExitCode        *int    `json:"exit_code,omitempty"`
	SessionID       *int    `json:"session_id,omitempty"`
	Output          string  `json:"output"`
}

func ExecCommand(ctx context.Context, ws *workspace.Workspace, manager *Manager, in ExecCommandInput) (*UnifiedExecOutput, error) {
	sessionID, err := manager.StartSession(ws, processInput{
		command: in.Cmd,
		workdir: in.Workdir,
		timeout: timeoutSeconds(in.TimeoutMS),
		tty:     in.TTY,
	})
	if err != nil {
		return nil, fmt.Errorf("exec_command: %w", err)
	}

	wait := time.Duration(clampMilliseconds(in.YieldTimeMS, defaultExecYieldMS, maxExecYieldMS)) * time.Millisecond
	if err := manager.waitFor(ctx, sessionID, wait); err != nil {
		_ = manager.Cancel(sessionID)
		return nil, fmt.Errorf("exec_command: %w", err)
	}
	return manager.sessionOutput(sessionID)
}

func WriteStdin(ctx context.Context, manager *Manager, in WriteStdinInput) (*UnifiedExecOutput, error) {
	if len(in.Chars) > maxWriteStdinBytes {
		return nil, fmt.Errorf("write_stdin: chars exceeds 64 KiB limit")
	}
	if in.SessionID <= 0 {
		return nil, fmt.Errorf("write_stdin: session_id must be positive")
	}
	p, err := manager.lookup(in.SessionID)
	if err != nil {
		return nil, fmt.Errorf("write_stdin: %w", err)
	}

	if in.Chars == "" && !p.tty {
		if err := manager.Cancel(in.SessionID); err != nil {
			return nil, fmt.Errorf("write_stdin: %w", err)
		}
		return manager.sessionOutput(in.SessionID)
	}

	if in.Chars != "" {
		select {
		case <-p.doneCh:
			return manager.sessionOutput(in.SessionID)
		default:
		}
		if err := p.writeStdinBounded(ctx, in.Chars, writeStdinWriteTimeout); err != nil {
			select {
			case <-p.doneCh:
				return manager.sessionOutput(in.SessionID)
			default:
			}
			return nil, fmt.Errorf("write_stdin: %w", err)
		}
	}

	wait := time.Duration(clampMilliseconds(in.YieldTimeMS, defaultWriteYieldMS, maxWriteYieldMS)) * time.Millisecond
	if err := manager.waitFor(ctx, in.SessionID, wait); err != nil {
		return nil, fmt.Errorf("write_stdin: %w", err)
	}
	return manager.sessionOutput(in.SessionID)
}

func (m *Manager) sessionOutput(sessionID int) (*UnifiedExecOutput, error) {
	p, err := m.lookup(sessionID)
	if err != nil {
		return nil, err
	}
	elapsedMS := time.Since(p.started).Milliseconds()
	out := &UnifiedExecOutput{Output: p.consumeOutput()}
	select {
	case <-p.doneCh:
		elapsedMS = p.elapsedMS.Load()
		code := int(p.exitCode.Load())
		out.ExitCode = &code
	default:
		id := sessionID
		out.SessionID = &id
	}
	out.WallTimeSeconds = float64(elapsedMS) / 1000
	return out, nil
}

func clampMilliseconds(value, fallback, max int) int {
	if value <= 0 {
		return fallback
	}
	if value > max {
		return max
	}
	return value
}

func timeoutSeconds(timeoutMS int) int {
	if timeoutMS <= 0 {
		return defaultCommandTimeoutSeconds
	}
	maxMS := maxCommandTimeoutSeconds * 1000
	if timeoutMS > maxMS {
		timeoutMS = maxMS
	}
	return (timeoutMS + 999) / 1000
}
