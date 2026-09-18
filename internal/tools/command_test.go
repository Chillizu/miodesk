package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCommandShortRun(t *testing.T) {
	ws, root := newWS(t)
	writeFile(t, root, "marker.txt", "here\n")

	out, err := Command(context.Background(), ws, CommandInput{
		Command: "cat marker.txt && echo to-stderr >&2 && exit 3",
		Timeout: 30,
	})
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if out.ExitCode != 3 {
		t.Errorf("exit code = %d", out.ExitCode)
	}
	if out.Stdout != "here\n" {
		t.Errorf("stdout = %q", out.Stdout)
	}
	if !strings.Contains(out.Stderr, "to-stderr") {
		t.Errorf("stderr = %q", out.Stderr)
	}
	if out.CWD != "." || out.TimedOut || out.ElapsedMS < 0 {
		t.Errorf("output = %+v", out)
	}
}

func TestCommandCwdSubdirectory(t *testing.T) {
	ws, root := newWS(t)
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := Command(context.Background(), ws, CommandInput{Command: "pwd", Timeout: 30})
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if !strings.HasSuffix(out.Stdout, "sub\n") && out.Stdout != ".\n" {
		// cwd default is the root; sanity only
		t.Logf("pwd = %q", out.Stdout)
	}

	out, err = Command(context.Background(), ws, CommandInput{Command: "pwd", CWD: "sub", Timeout: 30})
	if err != nil {
		t.Fatalf("Command sub: %v", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(out.Stdout), "sub") {
		t.Errorf("sub cwd pwd = %q", out.Stdout)
	}
	if out.CWD != "sub" {
		t.Errorf("CWD = %q", out.CWD)
	}
}

func TestCommandRefusesSudoAndEscape(t *testing.T) {
	ws, _ := newWS(t)
	for _, command := range []string{"sudo rm -rf /", "echo ok; sudo true", "echo $(sudo true)", "doas true", "/usr/bin/sudoedit file", "pkexec true", "su -", "runuser -u root true", "runas.exe cmd"} {
		if _, err := Command(context.Background(), ws, CommandInput{Command: command}); err == nil {
			t.Errorf("privilege escalation must be refused in %q", command)
		}
	}
	if _, err := Command(context.Background(), ws, CommandInput{Command: "pwd", CWD: "../../"}); err == nil {
		t.Error("cwd escape must be rejected")
	}
	if _, err := Command(context.Background(), ws, CommandInput{Command: "   "}); err == nil {
		t.Error("empty command must be rejected")
	}
}

func TestCommandHonorsCallerCancellation(t *testing.T) {
	ws, _ := newWS(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := Command(ctx, ws, CommandInput{Command: "sleep 30", Timeout: 30})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Command error = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("caller cancellation took too long: %v", elapsed)
	}
}

func TestCommandTimeout(t *testing.T) {
	ws, _ := newWS(t)
	start := time.Now()
	out, err := Command(context.Background(), ws, CommandInput{
		Command: "sleep 30",
		Timeout: 1,
	})
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if !out.TimedOut {
		t.Error("expected timed_out=true")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("timeout kill took too long: %v", elapsed)
	}
}

func TestManagerLifecycle(t *testing.T) {
	ws, _ := newWS(t)
	m := NewManager()
	defer m.Shutdown()

	id, err := m.Start(ws, CommandInput{Command: "echo started; sleep 30"})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !strings.HasPrefix(id, "task-") {
		t.Errorf("id = %q", id)
	}

	// Poll while running.
	poll, err := m.Poll(id)
	if err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if poll.Status != "running" {
		t.Errorf("early status = %q", poll.Status)
	}
	if poll.ExitCode != nil {
		t.Errorf("running task exit code = %v, want nil", *poll.ExitCode)
	}

	// Wait for output to land, then cancel.
	deadline := time.Now().Add(5 * time.Second)
	for {
		poll, err = m.Poll(id)
		if err != nil {
			t.Fatalf("Poll: %v", err)
		}
		if strings.Contains(poll.Stdout, "started") || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !strings.Contains(poll.Stdout, "started") {
		t.Errorf("stdout never appeared: %q", poll.Stdout)
	}

	poll, err = m.Cancel(id)
	if err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if poll.Status != "done" {
		t.Errorf("status after cancel = %q", poll.Status)
	}
	if poll.ElapsedMS >= 30*1000 {
		t.Errorf("cancel did not kill the process: %dms", poll.ElapsedMS)
	}

	// Unknown task errors.
	if _, err := m.Poll("task-999"); err == nil {
		t.Error("unknown task must error")
	}

	// Short-lived task completes on its own.
	id2, err := m.Start(ws, CommandInput{Command: "echo quick", Timeout: 30})
	if err != nil {
		t.Fatalf("Start 2: %v", err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		poll, err = m.Poll(id2)
		if err != nil {
			t.Fatalf("Poll 2: %v", err)
		}
		if poll.Status == "done" || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if poll.Status != "done" || poll.ExitCode == nil || *poll.ExitCode != 0 || !strings.Contains(poll.Stdout, "quick") {
		t.Errorf("final poll = %+v", poll)
	}
	finishedElapsed := poll.ElapsedMS
	time.Sleep(25 * time.Millisecond)
	pollAgain, err := m.Poll(id2)
	if err != nil {
		t.Fatalf("Poll completed task: %v", err)
	}
	if pollAgain.ElapsedMS != finishedElapsed {
		t.Errorf("completed task elapsed time changed: %d -> %d", finishedElapsed, pollAgain.ElapsedMS)
	}

	// Cancelling a finished task is a no-op.
	if _, err := m.Cancel(id2); err != nil {
		t.Errorf("cancel of finished task: %v", err)
	}
}

func TestManagerCapsConcurrentTasks(t *testing.T) {
	ws, _ := newWS(t)
	m := NewManager()

	// Fill the manager with synthetic running handles so the cap can be tested
	// without spawning dozens of real child processes.
	m.mu.Lock()
	for i := 0; i < maxRunningProcs; i++ {
		id := "task-synthetic-" + strconv.Itoa(i)
		m.procs[id] = &procHandle{doneCh: make(chan struct{})}
		m.order = append(m.order, id)
	}
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		for _, p := range m.procs {
			select {
			case <-p.doneCh:
			default:
				close(p.doneCh)
			}
		}
		m.mu.Unlock()
	}()

	if _, err := m.Start(ws, CommandInput{Command: "true"}); err == nil || !strings.Contains(err.Error(), "cap") {
		t.Fatalf("Start beyond cap error = %v", err)
	}
}
