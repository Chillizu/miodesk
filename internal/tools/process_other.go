//go:build !windows && !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package tools

import "os/exec"

// Unsupported platforms retain the standard context cancellation behavior.
func configureProcess(cmd *exec.Cmd) {}
