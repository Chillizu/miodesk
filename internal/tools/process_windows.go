//go:build windows

package tools

import "os/exec"

// Windows uses exec.CommandContext's process termination. A Windows Job
// Object can provide descendant-tree cancellation, but would require a
// platform-specific handle lifecycle beyond this release's scope.
func configureProcess(cmd *exec.Cmd) {}
