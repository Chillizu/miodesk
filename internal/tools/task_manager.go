package tools

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Chillizu/miodesk/internal/workspace"
)

const (
	// maxLongOutput caps captured output for resumable exec sessions.
	maxLongOutput = 1 << 20
	// maxRunningProcs prevents a client from exhausting the host with sessions.
	maxRunningProcs = 32
	// maxFinishedProcs bounds remembered completed sessions.
	maxFinishedProcs = 50
	// commandShutdownTimeout prevents a stuck child from blocking server exit.
	commandShutdownTimeout = 5 * time.Second
)

// Manager owns resumable exec sessions for one server process.
type Manager struct {
	mu    sync.Mutex
	next  int
	procs map[int]*procHandle
	order []int // creation order, for shutdown and eviction
}

// NewManager creates an empty exec session manager.
func NewManager() *Manager {
	return &Manager{procs: map[int]*procHandle{}}
}

// StartSession launches a resumable model-facing process with writable stdin.
func (m *Manager) StartSession(ws *workspace.Workspace, in processInput) (int, error) {
	p, err := newProc(context.Background(), ws, in, maxLongOutput)
	if err != nil {
		return 0, err
	}

	m.mu.Lock()
	if m.runningCountLocked() >= maxRunningProcs {
		m.mu.Unlock()
		p.abortBeforeStart()
		return 0, fmt.Errorf("already running %d exec sessions (cap %d); resume or cancel an existing session first", maxRunningProcs, maxRunningProcs)
	}
	if err := p.start(); err != nil {
		m.mu.Unlock()
		p.abortBeforeStart()
		return 0, fmt.Errorf("start exec session: %w", err)
	}
	m.next++
	id := m.next
	m.procs[id] = p
	m.order = append(m.order, id)
	m.evictLocked()
	m.mu.Unlock()

	go func() {
		waitWithTimeout(p, normalizeCommandTimeout(in.timeout))
		p.exitCode.Store(int32(exitCodeOf(p)))
		p.elapsedMS.Store(time.Since(p.started).Milliseconds())
		close(p.doneCh)
	}()
	return id, nil
}

func (m *Manager) waitFor(ctx context.Context, id int, wait time.Duration) error {
	p, err := m.lookup(id)
	if err != nil {
		return err
	}
	if wait < 0 {
		wait = 0
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-p.doneCh:
		return nil
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Cancel stops a live session. Cancelling an already-finished session is a no-op.
func (m *Manager) Cancel(id int) error {
	p, err := m.lookup(id)
	if err != nil {
		return err
	}
	select {
	case <-p.doneCh:
		return nil
	default:
		p.cancel()
	}
	timer := time.NewTimer(commandShutdownTimeout)
	defer timer.Stop()
	select {
	case <-p.doneCh:
	case <-timer.C:
		// Process group cancellation is best effort; do not hang the caller.
	}
	return nil
}

// Shutdown cancels every live session with one global grace period. The
// shutdown bound must not grow linearly with the number of active sessions.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	procs := make([]*procHandle, 0, len(m.order))
	for _, id := range m.order {
		if p, ok := m.procs[id]; ok {
			procs = append(procs, p)
		}
	}
	m.mu.Unlock()
	shutdownProcesses(procs, commandShutdownTimeout)
}

func shutdownProcesses(procs []*procHandle, timeout time.Duration) {
	for _, p := range procs {
		if p == nil || p.doneCh == nil {
			continue
		}
		select {
		case <-p.doneCh:
		default:
			if p.cancel != nil {
				p.cancel()
			}
		}
	}
	if timeout <= 0 {
		return
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for _, p := range procs {
		if p == nil || p.doneCh == nil {
			continue
		}
		select {
		case <-p.doneCh:
		case <-timer.C:
			return
		}
	}
}

func (m *Manager) lookup(id int) (*procHandle, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.procs[id]
	if !ok {
		return nil, fmt.Errorf("unknown exec session %d", id)
	}
	return p, nil
}

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

// evictLocked drops the oldest finished sessions beyond the memory cap.
// Callers must hold m.mu.
func (m *Manager) evictLocked() {
	for {
		doneCount := 0
		oldestDone := 0
		for _, id := range m.order {
			p, ok := m.procs[id]
			if !ok {
				continue
			}
			select {
			case <-p.doneCh:
				doneCount++
				if oldestDone == 0 {
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
