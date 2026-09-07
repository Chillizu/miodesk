// Package buildinfo carries build-time version metadata, overridable via
// -ldflags "-X github.com/Chillizu/miodesk/internal/buildinfo.Version=...".
package buildinfo

import "runtime"

var (
	Version   = "0.2.0-dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

func Platform() string { return runtime.GOOS + "/" + runtime.GOARCH }
