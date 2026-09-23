package tools

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Chillizu/miodesk/internal/workspace"
)

const (
	defaultCommandTimeoutSeconds = 120
	maxCommandTimeoutSeconds     = 600
)

type processInput struct {
	command string
	workdir string
	timeout int
	tty     bool
}

func shell() (string, string) {
	if runtime.GOOS == "windows" {
		return "cmd", "/c"
	}
	return "/bin/sh", "-c"
}

type procHandle struct {
	started time.Time
	cancel  context.CancelFunc
	cmd     *exec.Cmd
	tty     bool

	output limitedBuffer

	stdinMu sync.Mutex
	// stdin is installed before the session is published and is never replaced.
	stdin          io.WriteCloser
	stdinCloseOnce sync.Once
	stdinClosed    atomic.Bool
	stdinCloseErr  error
	pty            io.ReadWriteCloser

	outputCursorMu sync.Mutex
	outputCursor   int
	outputDone     chan struct{}

	// Completion fields are written once right before doneCh closes; readers
	// observe them only after seeing doneCh closed.
	exitCode  atomic.Int32
	elapsedMS atomic.Int64
	doneCh    chan struct{}
}

func newProc(parent context.Context, ws *workspace.Workspace, in processInput, capBytes int) (*procHandle, error) {
	if strings.TrimSpace(in.command) == "" {
		return nil, fmt.Errorf("command must not be empty")
	}
	if containsPrivilegeEscalation(in.command) {
		return nil, fmt.Errorf("refusing privilege escalation commands (sudo/doas/su/pkexec/runas)")
	}
	dir, err := ws.Resolve(in.workdir)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("workdir is not a directory: %s", ws.Rel(dir))
	}

	shellBin, flag := shell()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	cmd := exec.CommandContext(ctx, shellBin, flag, in.command)
	cmd.Dir = dir
	// WaitDelay closes ordinary pipes even when grandchildren keep them open.
	cmd.WaitDelay = 3 * time.Second

	p := &procHandle{
		started: time.Now(),
		cancel:  cancel,
		cmd:     cmd,
		tty:     in.tty,
		output:  limitedBuffer{max: capBytes},
		doneCh:  make(chan struct{}),
	}
	if in.tty {
		configurePTYProcess(cmd)
		ensureTerminalEnv(cmd)
		return p, nil
	}

	configureProcess(cmd)
	cmd.Stdout = &p.output
	cmd.Stderr = &p.output
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	p.stdin = stdin
	return p, nil
}

// containsPrivilegeEscalation conservatively rejects common privilege
// escalation commands anywhere in arbitrary shell text, including chained
// commands and substitutions. It is intentionally broader than a shell
// parser: refusing a quoted mention is safer than allowing an easy bypass.
// This is a guardrail, not a sandbox for the user's account.
func containsPrivilegeEscalation(command string) bool {
	for _, token := range []string{"sudoedit", "sudo", "doas", "pkexec", "runuser", "runas", "su"} {
		for i := 0; i+len(token) <= len(command); i++ {
			if !strings.EqualFold(command[i:i+len(token)], token) {
				continue
			}
			if i > 0 && isShellWordByte(command[i-1]) {
				continue
			}
			if i+len(token) < len(command) && isShellWordByte(command[i+len(token)]) {
				continue
			}
			return true
		}
	}
	return false
}

func isShellWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_'
}

func ensureTerminalEnv(cmd *exec.Cmd) {
	term := strings.TrimSpace(os.Getenv("TERM"))
	if term != "" && !strings.EqualFold(term, "dumb") {
		return
	}
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
}

func (p *procHandle) start() error {
	if !p.tty {
		return p.cmd.Start()
	}
	terminal, err := startPTY(p.cmd)
	if err != nil {
		return err
	}
	p.pty = terminal
	p.stdin = terminal
	p.outputDone = make(chan struct{})
	go func() {
		_, _ = io.Copy(&p.output, terminal)
		close(p.outputDone)
	}()
	return nil
}

// waitWithTimeout waits for the process and cancels it when its total runtime
// reaches the configured bound.
func waitWithTimeout(p *procHandle, timeoutSecs int) {
	timer := time.AfterFunc(time.Duration(timeoutSecs)*time.Second, p.cancel)
	defer timer.Stop()
	_ = p.cmd.Wait()
	p.finishOutput()
}

func (p *procHandle) finishOutput() {
	if p.pty == nil {
		return
	}
	// A normal PTY reaches EOF/EIO once the controlling process exits. Give the
	// reader a brief chance to drain trailing bytes, then close the master so a
	// descendant that inherited the terminal cannot keep the session alive.
	select {
	case <-p.outputDone:
	case <-time.After(100 * time.Millisecond):
		_ = p.pty.Close()
		select {
		case <-p.outputDone:
		case <-time.After(100 * time.Millisecond):
		}
	}
	_ = p.pty.Close()
}

func exitCodeOf(p *procHandle) int {
	if p.cmd.ProcessState == nil {
		return -1
	}
	return p.cmd.ProcessState.ExitCode()
}

func normalizeCommandTimeout(t int) int {
	if t <= 0 {
		return defaultCommandTimeoutSeconds
	}
	if t > maxCommandTimeoutSeconds {
		return maxCommandTimeoutSeconds
	}
	return t
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
	return string(append([]byte(nil), l.buf.Bytes()...))
}

func (l *limitedBuffer) Truncated() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.truncated
}

func (l *limitedBuffer) StringFrom(offset int) (string, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if offset < 0 || offset > l.buf.Len() {
		offset = 0
	}
	data := l.buf.Bytes()
	return string(append([]byte(nil), data[offset:]...)), len(data)
}

func (p *procHandle) consumeOutput() string {
	p.outputCursorMu.Lock()
	defer p.outputCursorMu.Unlock()
	out, next := p.output.StringFrom(p.outputCursor)
	p.outputCursor = next
	return out
}

func (p *procHandle) abortBeforeStart() {
	if p.cancel != nil {
		p.cancel()
	}
	_ = p.closeStdin()
}

func (p *procHandle) closeStdin() error {
	p.stdinCloseOnce.Do(func() {
		p.stdinClosed.Store(true)
		if p.stdin != nil {
			p.stdinCloseErr = p.stdin.Close()
		}
	})
	return p.stdinCloseErr
}

func (p *procHandle) writeStdinBounded(ctx context.Context, chars string, timeout time.Duration) error {
	if p.stdinClosed.Load() {
		return fmt.Errorf("session stdin is closed")
	}
	if !p.stdinMu.TryLock() {
		return fmt.Errorf("session stdin write already in progress")
	}
	defer p.stdinMu.Unlock()
	if p.stdinClosed.Load() {
		return fmt.Errorf("session stdin is closed")
	}
	if p.stdin == nil {
		return fmt.Errorf("session stdin is not writable")
	}
	if chars == "" {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("stdin write canceled: %w", err)
	}
	if timeout <= 0 {
		timeout = writeStdinWriteTimeout
	}

	stdin := p.stdin
	done := make(chan error, 1)
	go func() {
		written, err := io.WriteString(stdin, chars)
		if err == nil && written != len(chars) {
			err = io.ErrShortWrite
		}
		done <- err
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		p.interruptStdinWrite(done)
		return fmt.Errorf("stdin write canceled: %w", ctx.Err())
	case <-timer.C:
		p.interruptStdinWrite(done)
		return fmt.Errorf("stdin write timed out after %s", timeout)
	}
}

func (p *procHandle) interruptStdinWrite(done <-chan error) {
	_ = p.closeStdin()
	if waitForStdinWrite(done, 100*time.Millisecond) {
		return
	}
	if p.cancel != nil {
		p.cancel()
	}
	_ = waitForStdinWrite(done, 100*time.Millisecond)
}

func waitForStdinWrite(done <-chan error, timeout time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}
