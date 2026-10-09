package tunnel

import (
	"os/exec"
	"time"
)

const processStopTimeout = 5 * time.Second

// stopAndWait terminates a provider process and gives its Wait goroutine one
// bounded grace period to finish. Providers share this lifecycle so stop
// behavior cannot drift independently.
func stopAndWait(cmd *exec.Cmd, done <-chan struct{}) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	if done != nil {
		select {
		case <-done:
			return
		default:
		}
	}
	stopProcess(cmd)
	if done == nil {
		return
	}
	timer := time.NewTimer(processStopTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
	}
}
