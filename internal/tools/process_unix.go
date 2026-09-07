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
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
