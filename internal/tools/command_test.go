package tools

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestUnifiedExecShortRun(t *testing.T) {
	ws, root := newWS(t)
	writeFile(t, root, "marker.txt", "here\n")
	m := NewManager()
	defer m.Shutdown()

	out, err := ExecCommand(context.Background(), ws, m, ExecCommandInput{
		Cmd:         "cat marker.txt && echo to-stderr >&2 && exit 3",
		YieldTimeMS: 1000,
		TimeoutMS:   30_000,
	})
	if err != nil {
		t.Fatalf("ExecCommand: %v", err)
	}
	if out.ExitCode == nil || *out.ExitCode != 3 || out.SessionID != nil {
		t.Errorf("completion = %+v", out)
	}
	if !strings.Contains(out.Output, "here\n") || !strings.Contains(out.Output, "to-stderr") {
		t.Errorf("output = %q", out.Output)
	}
}

func TestUnifiedExecWorkdirSubdirectory(t *testing.T) {
	ws, root := newWS(t)
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := NewManager()
	defer m.Shutdown()

	out, err := ExecCommand(context.Background(), ws, m, ExecCommandInput{
		Cmd:         "pwd",
		Workdir:     "sub",
		YieldTimeMS: 1000,
	})
	if err != nil {
		t.Fatalf("ExecCommand: %v", err)
	}
	if !strings.HasSuffix(strings.TrimSpace(out.Output), "sub") {
		t.Errorf("sub workdir pwd = %q", out.Output)
	}
}

func TestUnifiedExecRefusesPrivilegeEscalationAndEscape(t *testing.T) {
	ws, _ := newWS(t)
	m := NewManager()
	defer m.Shutdown()

	for _, command := range []string{
		"sudo rm -rf /",
		"echo ok; sudo true",
		"echo $(sudo true)",
		"doas true",
		"/usr/bin/sudoedit file",
		"pkexec true",
		"su -",
		"runuser -u root true",
		"runas.exe cmd",
	} {
		if _, err := ExecCommand(context.Background(), ws, m, ExecCommandInput{Cmd: command}); err == nil {
			t.Errorf("privilege escalation must be refused in %q", command)
		}
	}
	if _, err := ExecCommand(context.Background(), ws, m, ExecCommandInput{Cmd: "pwd", Workdir: "../../"}); err == nil {
		t.Error("workdir escape must be rejected")
	}
	if _, err := ExecCommand(context.Background(), ws, m, ExecCommandInput{Cmd: "   "}); err == nil {
		t.Error("empty command must be rejected")
	}
}

func TestUnifiedExecHonorsCallerCancellation(t *testing.T) {
	ws, _ := newWS(t)
	m := NewManager()
	defer m.Shutdown()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err := ExecCommand(ctx, ws, m, ExecCommandInput{
		Cmd:         "sleep 30",
		YieldTimeMS: 30_000,
		TimeoutMS:   30_000,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ExecCommand error = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("caller cancellation took too long: %v", elapsed)
	}
}

func TestUnifiedExecTotalTimeout(t *testing.T) {
	ws, _ := newWS(t)
	m := NewManager()
	defer m.Shutdown()
	start := time.Now()

	out, err := ExecCommand(context.Background(), ws, m, ExecCommandInput{
		Cmd:         "sleep 30",
		YieldTimeMS: 5000,
		TimeoutMS:   1000,
	})
	if err != nil {
		t.Fatalf("ExecCommand: %v", err)
	}
	if out.SessionID != nil || out.ExitCode == nil {
		t.Errorf("timed out command should be completed: %+v", out)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("timeout kill took too long: %v", elapsed)
	}
}

func TestManagerWaitHonorsContextCancellation(t *testing.T) {
	ws, _ := newWS(t)
	m := NewManager()
	defer m.Shutdown()

	id, err := m.StartSession(ws, processInput{command: "sleep 30", timeout: 30})
	if err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	err = m.waitFor(ctx, id, 30*time.Second)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waitFor error = %v, want context deadline exceeded", err)
	}
	p, err := m.lookup(id)
	if err != nil {
		t.Fatalf("lookup after wait cancellation: %v", err)
	}
	select {
	case <-p.doneCh:
		t.Error("wait cancellation must not cancel the underlying session")
	default:
	}
}

func TestUnifiedExecSessionInputAndCancel(t *testing.T) {
	ws, _ := newWS(t)
	m := NewManager()
	defer m.Shutdown()

	out, err := ExecCommand(context.Background(), ws, m, ExecCommandInput{
		Cmd:         "read line; printf 'got:%s\\n' \"$line\"; sleep 30",
		YieldTimeMS: 10,
		TimeoutMS:   30_000,
	})
	if err != nil {
		t.Fatalf("ExecCommand: %v", err)
	}
	if out.SessionID == nil || *out.SessionID <= 0 {
		t.Fatalf("expected live session, got %+v", out)
	}
	id := *out.SessionID

	out, err = WriteStdin(context.Background(), m, WriteStdinInput{
		SessionID:   id,
		Chars:       "hello\n",
		YieldTimeMS: 100,
	})
	if err != nil {
		t.Fatalf("WriteStdin input: %v", err)
	}
	if !strings.Contains(out.Output, "got:hello") {
		t.Fatalf("stdin output = %+v", out)
	}
	if out.SessionID == nil {
		t.Fatalf("session should still be live after input: %+v", out)
	}

	out, err = WriteStdin(context.Background(), m, WriteStdinInput{
		SessionID:   id,
		Chars:       "\x03",
		YieldTimeMS: 10,
	})
	if err != nil {
		t.Fatalf("WriteStdin cancel: %v", err)
	}
	if out.SessionID != nil || out.ExitCode == nil {
		t.Fatalf("cancelled session = %+v", out)
	}
}

func TestWriteStdinRejectsOversizedInput(t *testing.T) {
	ws, _ := newWS(t)
	m := NewManager()
	defer m.Shutdown()

	out, err := ExecCommand(context.Background(), ws, m, ExecCommandInput{
		Cmd:         "read line; sleep 30",
		YieldTimeMS: 10,
		TimeoutMS:   30_000,
	})
	if err != nil {
		t.Fatalf("ExecCommand: %v", err)
	}
	if out.SessionID == nil {
		t.Fatalf("expected live session, got %+v", out)
	}
	id := *out.SessionID

	_, err = WriteStdin(context.Background(), m, WriteStdinInput{
		SessionID: id,
		Chars:     strings.Repeat("x", (64<<10)+1),
	})
	if err == nil || !strings.Contains(err.Error(), "64 KiB") {
		t.Fatalf("oversized write error = %v, want clear 64 KiB limit", err)
	}

	out, err = WriteStdin(context.Background(), m, WriteStdinInput{SessionID: id, YieldTimeMS: 10})
	if err != nil {
		t.Fatalf("poll after rejected input: %v", err)
	}
	if out.SessionID == nil || *out.SessionID != id {
		t.Fatalf("session changed after rejected input: %+v", out)
	}
	if _, err := WriteStdin(context.Background(), m, WriteStdinInput{SessionID: id, Chars: "\x03"}); err != nil {
		t.Fatalf("cancel cleanup session: %v", err)
	}
}

type blockingWriteCloser struct {
	started    chan struct{}
	closed     chan struct{}
	finished   chan struct{}
	startOnce  sync.Once
	closeOnce  sync.Once
	finishOnce sync.Once
	writeCalls atomic.Int32
}

func newBlockingWriteCloser() *blockingWriteCloser {
	return &blockingWriteCloser{
		started:  make(chan struct{}),
		closed:   make(chan struct{}),
		finished: make(chan struct{}),
	}
}

func (b *blockingWriteCloser) Write([]byte) (int, error) {
	b.writeCalls.Add(1)
	b.startOnce.Do(func() { close(b.started) })
	<-b.closed
	b.finishOnce.Do(func() { close(b.finished) })
	return 0, io.ErrClosedPipe
}

func (b *blockingWriteCloser) Close() error {
	b.closeOnce.Do(func() { close(b.closed) })
	return nil
}

func TestWriteStdinBoundedTimesOutAndRejectsConcurrentWrites(t *testing.T) {
	blocker := newBlockingWriteCloser()
	p := &procHandle{stdin: blocker}
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- p.writeStdinBounded(context.Background(), "blocked", 200*time.Millisecond)
	}()
	select {
	case <-blocker.started:
	case <-time.After(time.Second):
		t.Fatal("blocked writer did not start")
	}

	start := time.Now()
	err := p.writeStdinBounded(context.Background(), "second", time.Second)
	if err == nil || !strings.Contains(err.Error(), "in progress") {
		t.Fatalf("concurrent write error = %v, want in-progress error", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("concurrent write waited %s, want immediate rejection", elapsed)
	}

	select {
	case err := <-firstDone:
		if err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("blocked write error = %v, want timeout", err)
		}
	case <-time.After(time.Second):
		t.Fatal("bounded write did not return after timeout")
	}
	select {
	case <-blocker.closed:
	default:
		t.Fatal("timeout did not close stdin")
	}
	select {
	case <-blocker.finished:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("in-flight writer did not exit after stdin closed")
	}
	if got := blocker.writeCalls.Load(); got != 1 {
		t.Fatalf("writer calls = %d, want 1", got)
	}
	if !p.stdinMu.TryLock() {
		t.Fatal("bounded write left stdin serialization locked")
	}
	p.stdinMu.Unlock()

	start = time.Now()
	err = p.writeStdinBounded(context.Background(), "after close", time.Second)
	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("write after close error = %v, want closed-input error", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("write after close waited %s, want immediate rejection", elapsed)
	}
}

func TestWriteStdinBoundedHonorsRequestCancellation(t *testing.T) {
	blocker := newBlockingWriteCloser()
	p := &procHandle{stdin: blocker}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.writeStdinBounded(ctx, "blocked", time.Second) }()
	select {
	case <-blocker.started:
	case <-time.After(time.Second):
		t.Fatal("blocked writer did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("blocked write error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("bounded write did not return after request cancellation")
	}
	select {
	case <-blocker.closed:
	default:
		t.Fatal("request cancellation did not close stdin")
	}
	select {
	case <-blocker.finished:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("in-flight writer did not exit after request cancellation")
	}
}

func TestUnifiedExecConsumesOutputIncrementally(t *testing.T) {
	ws, _ := newWS(t)
	m := NewManager()
	defer m.Shutdown()

	out, err := ExecCommand(context.Background(), ws, m, ExecCommandInput{
		Cmd:         "echo first; sleep 1; echo second; sleep 30",
		YieldTimeMS: 50,
		TimeoutMS:   30_000,
	})
	if err != nil {
		t.Fatalf("ExecCommand: %v", err)
	}
	if out.SessionID == nil {
		t.Fatalf("expected live session: %+v", out)
	}
	id := *out.SessionID
	if !strings.Contains(out.Output, "first") {
		t.Fatalf("initial output = %q", out.Output)
	}

	out, err = WriteStdin(context.Background(), m, WriteStdinInput{
		SessionID:   id,
		YieldTimeMS: 1500,
	})
	if err != nil {
		t.Fatalf("WriteStdin: %v", err)
	}
	if strings.Contains(out.Output, "first") || !strings.Contains(out.Output, "second") {
		t.Fatalf("incremental output = %q", out.Output)
	}

	if _, err := WriteStdin(context.Background(), m, WriteStdinInput{
		SessionID: id,
		Chars:     "\x03",
	}); err != nil {
		t.Fatalf("cancel: %v", err)
	}
}

func TestProcAbortBeforeStartClosesInput(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	cancelled := false
	p := &procHandle{
		cancel: func() { cancelled = true },
		stdin:  writer,
	}

	p.abortBeforeStart()
	if !cancelled {
		t.Fatal("abortBeforeStart did not cancel the process context")
	}
	buf := make([]byte, 1)
	if _, err := reader.Read(buf); !errors.Is(err, io.EOF) {
		t.Fatalf("stdin pipe remained open after abort: %v", err)
	}
}

func TestShutdownProcessesUsesSingleDeadline(t *testing.T) {
	cancelled := 0
	procs := []*procHandle{
		{doneCh: make(chan struct{}), cancel: func() { cancelled++ }},
		{doneCh: make(chan struct{}), cancel: func() { cancelled++ }},
		{doneCh: make(chan struct{}), cancel: func() { cancelled++ }},
	}

	start := time.Now()
	shutdownProcesses(procs, 50*time.Millisecond)
	if cancelled != len(procs) {
		t.Fatalf("cancelled %d processes, want %d", cancelled, len(procs))
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Fatalf("shutdown deadline multiplied by process count: %v", elapsed)
	}
}

func TestManagerCapsConcurrentSessions(t *testing.T) {
	ws, _ := newWS(t)
	m := NewManager()

	// Fill the manager with synthetic running handles so the cap can be tested
	// without spawning dozens of real child processes.
	m.mu.Lock()
	for i := 1; i <= maxRunningProcs; i++ {
		m.procs[i] = &procHandle{doneCh: make(chan struct{})}
		m.order = append(m.order, i)
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

	if _, err := m.StartSession(ws, processInput{command: "true"}); err == nil || !strings.Contains(err.Error(), "cap") {
		t.Fatalf("StartSession beyond cap error = %v", err)
	}
}
