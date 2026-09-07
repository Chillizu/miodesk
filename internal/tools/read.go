// Package tools implements miodesk's file tools — read, search, list — on top
// of the workspace sandbox. Tools are host-agnostic: they know nothing about
// MCP, ChatGPT, or any other client.
package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"

	"miodesk/internal/workspace"
)

const (
	// MaxReadBytes caps how much of a file a single read returns. Larger
	// files are returned truncated with Truncated=true.
	MaxReadBytes = 512 << 10
	// DefaultReadLines is the line window when the caller passes no limit.
	DefaultReadLines = 2000
)

type ReadInput struct {
	Path   string `json:"path" jsonschema:"file path relative to the workspace root, or absolute inside it"`
	Offset int    `json:"offset,omitempty" jsonschema:"1-based line number to start from (default 1)"`
	Limit  int    `json:"limit,omitempty" jsonschema:"maximum number of lines to return (default 2000)"`
}

type ReadOutput struct {
	Kind   string `json:"kind"` // "read"
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Offset int    `json:"offset"`
	Lines  int    `json:"lines"`
	// Truncated reports whether file content continues beyond the returned
	// window (byte cap hit, or lines remain after the requested window).
	Truncated bool   `json:"truncated"`
	Content   string `json:"content"`
}

// Read returns a bounded window of a workspace file as text.
func Read(ctx context.Context, ws *workspace.Workspace, in ReadInput) (*ReadOutput, error) {
	path, err := ws.Resolve(in.Path)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("read: file not found: %s", ws.Rel(path))
		}
		return nil, err
	}
	if fi.IsDir() {
		return nil, fmt.Errorf("read: %s is a directory (use list)", ws.Rel(path))
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Read at most MaxReadBytes+1 so the extra byte marks truncation.
	buf := make([]byte, MaxReadBytes+1)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return nil, err
	}
	data := buf[:n]
	truncated := n > MaxReadBytes
	if truncated {
		data = data[:MaxReadBytes]
	}
	if bytes.IndexByte(head(data, 1024), 0) >= 0 {
		return nil, fmt.Errorf("read: %s looks like a binary file", ws.Rel(path))
	}

	offset := in.Offset
	if offset <= 0 {
		offset = 1
	}
	limit := in.Limit
	if limit <= 0 {
		limit = DefaultReadLines
	}

	var out strings.Builder
	lines := 0
	current := 1
	for len(data) > 0 && lines < limit {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var line string
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			line, data = string(data[:i+1]), data[i+1:]
		} else {
			line, data = string(data), nil
		}
		if current >= offset {
			out.WriteString(line)
			lines++
		}
		current++
	}
	// Lines beyond the byte window were dropped before the window closed.
	if truncated || (len(data) > 0 && lines == limit) {
		truncated = true
	}

	return &ReadOutput{
		Kind:      "read",
		Path:      ws.Rel(path),
		Size:      fi.Size(),
		Offset:    offset,
		Lines:     lines,
		Truncated: truncated,
		Content:   out.String(),
	}, nil
}

func head(b []byte, n int) []byte {
	if len(b) > n {
		return b[:n]
	}
	return b
}
