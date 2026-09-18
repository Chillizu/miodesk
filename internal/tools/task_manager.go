package tools

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/Chillizu/miodesk/internal/workspace"
)

const (
	// maxLongOutput caps captured output for long-running tasks.
	maxLongOutput = 1 << 20
	// maxRunningProcs prevents a client from exhausting the host by creating
	// unbounded long-running tasks. Finished tasks are retained separately.
	maxRunningProcs = 32
	// maxFinishedProcs bounds remembered completed long tasks.
	maxFinishedProcs = 50
	// commandShutdownTimeout prevents a stuck child from blocking server exit.
	commandShutdownTimeout = 5 * time.Second
)

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
	p, err := newProc(context.Background(), ws, in, maxLongOutput)
	if err != nil {
		return "", err
	}

	m.mu.Lock()
	if m.runningCountLocked() >= maxRunningProcs {
		m.mu.Unlock()
		p.cancel()
		return "", fmt.Errorf("command_start: already running %d long commands (cap %d); poll or cancel an existing task first", maxRunningProcs, maxRunningProcs)
	}
	if err := p.cmd.Start(); err != nil {
		m.mu.Unlock()
		p.cancel()
		return "", fmt.Errorf("command_start: %w", err)
	}
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
		p.elapsedMS.Store(time.Since(p.started).Milliseconds())
		close(p.doneCh)
	}()
	return p.id, nil
}

// runningCountLocked counts tasks whose completion channel is still open.
// Callers must hold m.mu.
func (m *Manager) runningCountLocked() int {
	count := 0
	for _, p := range m.procs {
		select {
		case <-p.doneCh:
		default:
			count++
		}
	}
	return count
}

// Poll returns the accumulated status and output of one task.
func (m *Manager) Poll(id string) (*PollOutput, error) {
	p, err := m.lookup(id)
	if err != nil {
		return nil, fmt.Errorf("command_poll: %w", err)
	}
	status := "running"
	elapsedMS := time.Since(p.started).Milliseconds()
	var exitCode *int
	select {
	case <-p.doneCh:
		status = "done"
		elapsedMS = p.elapsedMS.Load()
		code := int(p.exitCode.Load())
		exitCode = &code
	default:
	}
	return &PollOutput{
		Kind:            "command",
		ID:              p.id,
		Status:          status,
		ExitCode:        exitCode,
		Stdout:          p.stdout.String(),
		Stderr:          p.stderr.String(),
		StdoutTruncated: p.stdout.Truncated(),
		StderrTruncated: p.stderr.Truncated(),
		TimedOut:        p.timedOut.Load(),
		ElapsedMS:       elapsedMS,
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
			select {
			case <-p.doneCh:
			case <-time.After(commandShutdownTimeout):
				// The process is expected to be gone after group cancellation;
				// do not make server shutdown hang forever if the OS refuses it.
			}
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
		for i, id := range m.order {
			if id == oldestDone {
				m.order = append(m.order[:i], m.order[i+1:]...)
				break
			}
		}
	}
}

type PollOutput struct {
	Kind            string `json:"kind"` // "command"
	ID              string `json:"id"`
	Status          string `json:"status"` // running | done
	ExitCode        *int   `json:"exit_code"`
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
	Kind string `json:"kind"` // "command"
	ID   string `json:"id"`
}
