//go:build !windows && !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package tunnel

import "os/exec"

func configureProcess(cmd *exec.Cmd) {}

func stopProcess(cmd *exec.Cmd) {
	if cmd.Cancel != nil {
		_ = cmd.Cancel()
		return
	}
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
