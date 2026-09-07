// Package buildinfo carries build-time version metadata, overridable via
// -ldflags "-X miodesk/internal/buildinfo.Version=...".
package buildinfo

import "runtime"

var (
	Version   = "0.1.0-dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

func Platform() string { return runtime.GOOS + "/" + runtime.GOARCH }
