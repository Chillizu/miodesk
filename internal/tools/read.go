// Package tools implements miodesk's file tools — read, search, list — on top
// of the workspace sandbox. Tools are host-agnostic: they know nothing about
// MCP, ChatGPT, or any other client.
package tools

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Chillizu/miodesk/internal/workspace"
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
	Offset int    `json:"offset,omitempty" jsonschema:"1-based start line, valid anywhere in file (default 1)"`
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

	// Stream to the requested line window rather than always taking the first
	// 512 KiB. Skipped lines and long lines never need to fit in memory.
	r := bufio.NewReader(f)
	prefix, err := r.Peek(1024)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if bytes.IndexByte(prefix, 0) >= 0 {
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
	lines, current := 0, 1
	inLine, truncated := false, false
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		segment, readErr := r.ReadSlice('\n')
		if len(segment) == 0 && errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil && !errors.Is(readErr, bufio.ErrBufferFull) && !errors.Is(readErr, io.EOF) {
			return nil, readErr
		}
		if current >= offset {
			if !inLine {
				lines++
				inLine = true
			}
			room := MaxReadBytes - out.Len()
			if len(segment) > room {
				out.Write(segment[:room])
				truncated = true
				break
			}
			out.Write(segment)
		}
		if readErr == nil || errors.Is(readErr, io.EOF) {
			if lines >= limit {
				// A complete final line is not truncated if it reaches EOF.
				if _, err := r.Peek(1); err == nil {
					truncated = true
				} else if !errors.Is(err, io.EOF) {
					return nil, err
				}
				break
			}
			current++
			inLine = false
			if errors.Is(readErr, io.EOF) {
				break
			}
		}
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
