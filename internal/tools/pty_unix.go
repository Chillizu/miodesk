//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package tools

import (
	"fmt"
	"io"
	"os/exec"

	"github.com/creack/pty"
)

const (
	defaultPTYRows = 40
	defaultPTYCols = 120
)

func startPTY(cmd *exec.Cmd) (io.ReadWriteCloser, error) {
	terminal, err := pty.StartWithSize(cmd, &pty.Winsize{
		Rows: defaultPTYRows,
		Cols: defaultPTYCols,
	})
	if err != nil {
		return nil, fmt.Errorf("start PTY: %w", err)
	}
	return terminal, nil
}
