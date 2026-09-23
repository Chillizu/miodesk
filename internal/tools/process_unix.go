//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package tools

import (
	"os/exec"
	"syscall"
)

// configureProcess makes cancellation apply to the shell and every child it
// starts. Commands are intentionally still run as the current user; this only
// makes the existing timeout/cancel contract reliable for shell descendants.
func configureProcess(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = killProcessGroup(cmd)
}

// PTY startup creates a fresh session and controlling terminal itself. Keep
// SysProcAttr free for the PTY package, but retain process-group cancellation:
// the session leader is also the process-group leader, so -pid is the group.
func configurePTYProcess(cmd *exec.Cmd) {
	cmd.Cancel = killProcessGroup(cmd)
}

func killProcessGroup(cmd *exec.Cmd) func() error {
	return func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
