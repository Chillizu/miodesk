//go:build !linux

package codemode

import "errors"

// Unsupported platforms fail closed until an equivalent memory guard is
// implemented and verified there.
func enforceWorkerMemoryLimit() error {
	return errors.New("codemode: verified worker memory limit is unavailable on this platform")
}
