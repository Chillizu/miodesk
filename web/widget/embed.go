// Package widget embeds the miodesk widget UI. The assets ship inside the
// binary — release builds require no Node, no bundler, no external files.
package widget

import "embed"

// Static holds index.html, style.css, and app.js served at "/".
//
//go:embed static
var Static embed.FS
