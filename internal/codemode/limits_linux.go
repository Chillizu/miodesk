//go:build linux

package codemode

import "syscall"

// RLIMIT_DATA bounds the Goja worker's heap/mmap growth on modern Linux.
// This is a memory resource limit, not a filesystem or network sandbox.
func enforceWorkerMemoryLimit() error {
	// Allow runtime/allocator variance across Linux hosts while retaining
	// an OS-enforced per-worker limit (oversized JS allocations still fail).
	const maxData = 256 << 20
	if err := syscall.Setrlimit(syscall.RLIMIT_CORE, &syscall.Rlimit{Cur: 0, Max: 0}); err != nil {
		return err
	}
	return syscall.Setrlimit(syscall.RLIMIT_DATA, &syscall.Rlimit{Cur: maxData, Max: maxData})
}
