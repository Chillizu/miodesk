//go:build linux

package tools

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCommandTimeoutKillsShellDescendants(t *testing.T) {
	ws, root := newWS(t)
	out, err := Command(context.Background(), ws, CommandInput{
		Command: "sleep 30 & child=$!; printf '%s' \"$child\" > child.pid; wait",
		Timeout: 1,
	})
	if err != nil {
		t.Fatalf("Command: %v", err)
	}
	if !out.TimedOut {
		t.Fatal("expected timed_out=true")
	}
	data, err := os.ReadFile(filepath.Join(root, "child.pid"))
	if err != nil {
		t.Fatalf("child pid file: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatalf("child pid %q: %v", data, err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		err = syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if err != nil {
			t.Fatalf("probe child pid %d: %v", pid, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("shell descendant pid %d is still alive", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
