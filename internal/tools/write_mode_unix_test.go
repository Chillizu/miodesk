//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package tools

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestWriteFileAtomicHonorsUmask(t *testing.T) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)

	dir := t.TempDir()
	createdPath := filepath.Join(dir, "created.txt")
	if err := writeFileAtomic(createdPath, []byte("created")); err != nil {
		t.Fatalf("writeFileAtomic create: %v", err)
	}
	created, err := os.Stat(createdPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := created.Mode().Perm(); got != 0o600 {
		t.Errorf("new file mode = %04o, want umask-filtered 0600", got)
	}

	existingPath := filepath.Join(dir, "existing.txt")
	if err := os.WriteFile(existingPath, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(existingPath, []byte("after")); err != nil {
		t.Fatalf("writeFileAtomic overwrite: %v", err)
	}
	existing, err := os.Stat(existingPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := existing.Mode().Perm(); got != 0o600 {
		t.Errorf("overwritten file mode = %04o, want preserved 0600", got)
	}
}
