//go:build linux

package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestUnifiedExecPTYIsInteractive(t *testing.T) {
	ws, _ := newWS(t)
	m := NewManager()
	defer m.Shutdown()

	out, err := ExecCommand(context.Background(), ws, m, ExecCommandInput{
		Cmd:         `if [ -t 0 ] && [ -t 1 ]; then echo tty-ok; else echo no-tty; fi; read line; printf 'got:%s\n' "$line"; trap 'echo interrupted; exit 0' INT; while :; do sleep 30; done`,
		TTY:         true,
		YieldTimeMS: 100,
		TimeoutMS:   30_000,
	})
	if err != nil {
		t.Fatalf("ExecCommand PTY: %v", err)
	}
	if out.SessionID == nil {
		t.Fatalf("expected live PTY session: %+v", out)
	}
	if !strings.Contains(out.Output, "tty-ok") {
		t.Fatalf("PTY detection output = %q", out.Output)
	}
	id := *out.SessionID

	out, err = WriteStdin(context.Background(), m, WriteStdinInput{
		SessionID:   id,
		Chars:       "hello\n",
		YieldTimeMS: 250,
	})
	if err != nil {
		t.Fatalf("WriteStdin PTY input: %v", err)
	}
	if !strings.Contains(out.Output, "got:hello") {
		t.Fatalf("PTY stdin output = %q", out.Output)
	}
	if out.SessionID == nil {
		t.Fatalf("PTY session ended before interrupt: %+v", out)
	}

	out, err = WriteStdin(context.Background(), m, WriteStdinInput{
		SessionID:   id,
		Chars:       "\x03",
		YieldTimeMS: 1000,
	})
	if err != nil {
		t.Fatalf("WriteStdin PTY Ctrl-C: %v", err)
	}
	if out.SessionID != nil {
		t.Fatalf("PTY Ctrl-C should let the command handle SIGINT and exit: %+v", out)
	}
	if out.ExitCode == nil || *out.ExitCode != 0 {
		t.Fatalf("PTY SIGINT trap exit = %+v", out)
	}
	if !strings.Contains(out.Output, "interrupted") {
		t.Fatalf("PTY Ctrl-C output = %q", out.Output)
	}
}

func TestUnifiedExecTimeoutKillsShellDescendants(t *testing.T) {
	ws, root := newWS(t)
	m := NewManager()
	defer m.Shutdown()

	out, err := ExecCommand(context.Background(), ws, m, ExecCommandInput{
		Cmd:         "sleep 30 & child=$!; printf '%s' \"$child\" > child.pid; wait",
		YieldTimeMS: 5000,
		TimeoutMS:   1000,
	})
	if err != nil {
		t.Fatalf("ExecCommand: %v", err)
	}
	if out.SessionID != nil || out.ExitCode == nil {
		t.Fatalf("timed out command should be completed: %+v", out)
	}
	data, err := os.ReadFile(filepath.Join(root, "child.pid"))
	if err != nil {
		t.Fatalf("child pid file: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("child pid %q: %v", data, err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		err = syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if err != nil {
			t.Fatalf("probe child pid %d: %v", pid, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("shell descendant pid %d is still alive", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
