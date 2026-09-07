//go:build windows

package cli

import "os/exec"

// Windows uses exec.CommandContext's process termination for this release.
func configureProcess(cmd *exec.Cmd) {}
