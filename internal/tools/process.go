package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Chillizu/miodesk/internal/workspace"
)

func shell() (string, string) {
	if runtime.GOOS == "windows" {
		return "cmd", "/c"
	}
	return "/bin/sh", "-c"
}

type procHandle struct {
	id      string
	dir     string
	relDir  string
	started time.Time

	ctx    context.Context
	cancel context.CancelFunc
	cmd    *exec.Cmd

	stdout limitedBuffer
	stderr limitedBuffer

	// Completion fields are written once right before doneCh closes; readers
	// observe them only after seeing doneCh closed.
	exitCode  atomic.Int32
	timedOut  atomic.Bool
	elapsedMS atomic.Int64
	doneCh    chan struct{}
}

var errTimedOut = errors.New("command timed out")

func newProc(parent context.Context, ws *workspace.Workspace, in CommandInput, capBytes int) (*procHandle, error) {
	if strings.TrimSpace(in.Command) == "" {
		return nil, fmt.Errorf("command: command must not be empty")
	}
	if containsPrivilegeEscalation(in.Command) {
		return nil, fmt.Errorf("command: refusing privilege escalation commands (sudo/doas/su/pkexec/runas)")
	}
	dir, err := ws.Resolve(in.CWD)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("command: cwd is not a directory: %s", ws.Rel(dir))
	}

	shellBin, flag := shell()
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	cmd := exec.CommandContext(ctx, shellBin, flag, in.Command)
	configureProcess(cmd)
	cmd.Dir = dir
	// WaitDelay closes the pipes even when grandchildren keep them open.
	cmd.WaitDelay = 3 * time.Second

	p := &procHandle{
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
	return string(append([]byte(nil), l.buf.Bytes()...))
}

func (l *limitedBuffer) Truncated() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.truncated
}
