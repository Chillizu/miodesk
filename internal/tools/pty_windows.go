//go:build windows

package tools

import (
	"fmt"
	"io"
	"os/exec"
)

func startPTY(cmd *exec.Cmd) (io.ReadWriteCloser, error) {
	return nil, fmt.Errorf("TTY execution is not supported on Windows yet")
}
