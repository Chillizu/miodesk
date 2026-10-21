//go:build linux

package codemode

import "syscall"

// RLIMIT_DATA bounds the Goja worker's heap/mmap growth on modern Linux.
// This is a memory resource limit, not a filesystem or network sandbox.
func enforceWorkerMemoryLimit() error {
	const maxData = 128 << 20
	if err := syscall.Setrlimit(syscall.RLIMIT_CORE, &syscall.Rlimit{Cur: 0, Max: 0}); err != nil {
		return err
	}
	return syscall.Setrlimit(syscall.RLIMIT_DATA, &syscall.Rlimit{Cur: maxData, Max: maxData})
}
