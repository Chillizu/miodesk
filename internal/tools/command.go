package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"miodesk/internal/workspace"
)

const (
	DefaultCommandTimeout = 120 // seconds
	MaxCommandTimeout     = 600
	// maxCommandOutput caps captured stdout/stderr for short commands.
	maxCommandOutput = 256 << 10
	// maxLongOutput caps captured output for long-running tasks.
	maxLongOutput = 1 << 20
	// maxFinishedProcs bounds remembered completed long tasks.
	maxFinishedProcs = 50
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
// stdout, stderr, the exit code, and elapsed time. sudo is refused.
func Command(ctx context.Context, ws *workspace.Workspace, in CommandInput) (*CommandOutput, error) {
	p, err := newProc(ws, in, maxCommandOutput)
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

func shell() (string, string) {
	if runtime.GOOS == "windows" {
		return "cmd", "/c"
	}
	return "/bin/sh", "-c"
}

// long-running command lifecycle -------------------------------------------
//
// Start returns a task id immediately; Poll reports status and accumulated
// output; Cancel kills the process group via context cancel. No HTTP request
// ever waits on a running process.

// Manager owns long-running tasks for one server process.
type Manager struct {
	mu    sync.Mutex
	next  int
	procs map[string]*procHandle
	order []string // creation order, for shutdown and eviction
}

// NewManager creates an empty task manager.
func NewManager() *Manager {
	return &Manager{procs: map[string]*procHandle{}}
}

// Start launches a command and returns its task id immediately.
func (m *Manager) Start(ws *workspace.Workspace, in CommandInput) (string, error) {
	p, err := newProc(ws, in, maxLongOutput)
	if err != nil {
		return "", err
	}
	if err := p.cmd.Start(); err != nil {
		p.cancel()
		return "", fmt.Errorf("command_start: %w", err)
	}

	m.mu.Lock()
	m.next++
	p.id = "task-" + strconv.Itoa(m.next)
	m.procs[p.id] = p
	m.order = append(m.order, p.id)
	m.evictLocked()
	m.mu.Unlock()

	go func() {
		waitErr := waitWithTimeout(p, DefaultTimeout(in.Timeout))
		p.exitCode.Store(int32(exitCodeOf(p)))
		p.timedOut.Store(waitErr == errTimedOut)
		close(p.doneCh)
	}()
	return p.id, nil
}

// Poll returns the accumulated status and output of one task.
func (m *Manager) Poll(id string) (*PollOutput, error) {
	p, err := m.lookup(id)
	if err != nil {
		return nil, fmt.Errorf("command_poll: %w", err)
	}
	status := "running"
	select {
	case <-p.doneCh:
		status = "done"
	default:
	}
	return &PollOutput{
		Kind:            "command",
		ID:              p.id,
		Command:         p.command,
		CWD:             p.relDir,
		Status:          status,
		ExitCode:        int(p.exitCode.Load()),
		Stdout:          p.stdout.String(),
		Stderr:          p.stderr.String(),
		StdoutTruncated: p.stdout.Truncated(),
		StderrTruncated: p.stderr.Truncated(),
		TimedOut:        p.timedOut.Load(),
		ElapsedMS:       time.Since(p.started).Milliseconds(),
	}, nil
}

// Cancel kills a running task; cancelling a finished task is a no-op.
func (m *Manager) Cancel(id string) (*PollOutput, error) {
	p, err := m.lookup(id)
	if err != nil {
		return nil, fmt.Errorf("command_cancel: %w", err)
	}
	select {
	case <-p.doneCh:
	default:
		p.cancel()
		select {
		case <-p.doneCh:
		case <-time.After(5 * time.Second):
			// Process ignored the kill; report state as-is.
		}
	}
	return m.Poll(id)
}

// Shutdown cancels every task; called on server exit.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	ids := append([]string(nil), m.order...)
	m.mu.Unlock()
	for _, id := range ids {
		m.mu.Lock()
		p, ok := m.procs[id]
		m.mu.Unlock()
		if !ok {
			continue
		}
		select {
		case <-p.doneCh:
		default:
			p.cancel()
			<-p.doneCh
		}
	}
}

func (m *Manager) lookup(id string) (*procHandle, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.procs[id]
	if !ok {
		return nil, fmt.Errorf("unknown task %q", id)
	}
	return p, nil
}

// evictLocked drops the oldest finished tasks beyond the memory cap.
// Callers must hold m.mu.
func (m *Manager) evictLocked() {
	for {
		doneCount := 0
		var oldestDone string
		for _, id := range m.order {
			p, ok := m.procs[id]
			if !ok {
				continue
			}
			select {
			case <-p.doneCh:
				doneCount++
				if oldestDone == "" {
					oldestDone = id
				}
			default:
			}
		}
		if doneCount <= maxFinishedProcs {
			return
		}
		delete(m.procs, oldestDone)
	}
}

type procHandle struct {
	id      string
	command string
	dir     string
	relDir  string
	started time.Time

	ctx    context.Context
	cancel context.CancelFunc
	cmd    *exec.Cmd

	stdout limitedBuffer
	stderr limitedBuffer

	// exitCode/timedOut are written once right before doneCh closes; readers
	// observe them only after seeing doneCh closed.
	exitCode atomic.Int32
	timedOut atomic.Bool
	doneCh   chan struct{}
}

type PollOutput struct {
	Kind            string `json:"kind"` // "command"
	ID              string `json:"id"`
	Command         string `json:"command"`
	CWD             string `json:"cwd"`
	Status          string `json:"status"` // running | done
	ExitCode        int    `json:"exit_code"`
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	StdoutTruncated bool   `json:"stdout_truncated"`
	StderrTruncated bool   `json:"stderr_truncated"`
	TimedOut        bool   `json:"timed_out"`
	ElapsedMS       int64  `json:"elapsed_ms"`
}

// TaskID identifies a long-running task, as returned by command_start.
type TaskID struct {
	ID string `json:"id" jsonschema:"task id returned by command_start"`
}

// TaskStarted is the command_start result.
type TaskStarted struct {
	Kind    string `json:"kind"` // "command"
	ID      string `json:"id"`
	Command string `json:"command"`
}

// plumbing ------------------------------------------------------------------

var errTimedOut = fmt.Errorf("command timed out")

func newProc(ws *workspace.Workspace, in CommandInput, capBytes int) (*procHandle, error) {
	if strings.TrimSpace(in.Command) == "" {
		return nil, fmt.Errorf("command: command must not be empty")
	}
	if strings.HasPrefix(strings.TrimSpace(in.Command), "sudo") {
		return nil, fmt.Errorf("command: refusing to run sudo (escalation is never automatic)")
	}
	dir, err := ws.Resolve(in.CWD)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("command: cwd is not a directory: %s", ws.Rel(dir))
	}

	shellBin, flag := shell()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, shellBin, flag, in.Command)
	cmd.Dir = dir
	// WaitDelay closes the pipes even when grandchildren keep them open.
	cmd.WaitDelay = 3 * time.Second

	p := &procHandle{
		command: in.Command,
		dir:     dir,
		relDir:  ws.Rel(dir),
		started: time.Now(),
		ctx:     ctx,
		cancel:  cancel,
		stdout:  limitedBuffer{max: capBytes},
		stderr:  limitedBuffer{max: capBytes},
		doneCh:  make(chan struct{}),
	}
	cmd.Stdout = &p.stdout
	cmd.Stderr = &p.stderr
	p.cmd = cmd
	return p, nil
}

// waitWithTimeout waits for the process, killing it on timeout.
func waitWithTimeout(p *procHandle, timeoutSecs int) error {
	var timedOut atomic.Bool
	timer := time.AfterFunc(time.Duration(timeoutSecs)*time.Second, func() {
		timedOut.Store(true)
		p.cancel()
	})
	defer timer.Stop()
	waitErr := p.cmd.Wait()
	if waitErr != nil && timedOut.Load() {
		return errTimedOut
	}
	return waitErr
}

func exitCodeOf(p *procHandle) int {
	if p.cmd.ProcessState == nil {
		return -1
	}
	return p.cmd.ProcessState.ExitCode()
}

// limitedBuffer captures up to max bytes and flags truncation beyond that.
// Write always reports success so the child never blocks on a full pipe.
type limitedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if room := l.max - l.buf.Len(); room > 0 {
		if len(p) > room {
			l.buf.Write(p[:room])
			l.truncated = true
		} else {
			l.buf.Write(p)
		}
	} else {
		l.truncated = true
	}
	return len(p), nil
}

func (l *limitedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func (l *limitedBuffer) Truncated() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.truncated
}
