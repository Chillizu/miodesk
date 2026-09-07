package tools

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/Chillizu/miodesk/internal/workspace"
)

const (
	DefaultListDepth = 1
	MaxListDepth     = 8
	DefaultListLimit = 500
	MaxListLimit     = 5000
)

type ListInput struct {
	Path  string `json:"path,omitempty" jsonschema:"directory to list, relative to the workspace root (default: root)"`
	Depth int    `json:"depth,omitempty" jsonschema:"recursion depth (default 1, max 8)"`
	Limit int    `json:"limit,omitempty" jsonschema:"maximum number of entries to return (default 500)"`
}

type Entry struct {
	Name string `json:"name"`
	Path string `json:"path"` // workspace-relative
	Kind string `json:"kind"` // dir | file | symlink
	Size int64  `json:"size,omitempty"`
}

type ListOutput struct {
	Kind      string  `json:"kind"` // "list"
	Path      string  `json:"path"`
	Entries   []Entry `json:"entries"`
	Truncated bool    `json:"truncated"`
}

// List walks a workspace directory with bounded depth and entry count so a
// huge tree can never flood one response.
func List(ctx context.Context, ws *workspace.Workspace, in ListInput) (*ListOutput, error) {
	path, err := ws.Resolve(in.Path)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("list: directory not found: %s", ws.Rel(path))
		}
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("list: %s is not a directory", ws.Rel(path))
	}

	depth := in.Depth
	if depth <= 0 {
		depth = DefaultListDepth
	}
	if depth > MaxListDepth {
		depth = MaxListDepth
	}
	limit := in.Limit
	if limit <= 0 {
		limit = DefaultListLimit
	}
	if limit > MaxListLimit {
		limit = MaxListLimit
	}

	out := &ListOutput{Kind: "list", Path: ws.Rel(path), Entries: []Entry{}}
	// filepath.WalkDir never follows symlinks, so a symlinked directory is
	// reported but not descended into; targets go through the sandbox again.
	err = filepath.WalkDir(path, func(p string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel, rerr := filepath.Rel(path, p)
		if rerr != nil {
			return rerr
		}
		if rel != "." {
			if len(out.Entries) >= limit {
				out.Truncated = true
				return fs.SkipAll
			}
			e := Entry{Name: d.Name(), Path: rel, Kind: kindOf(d)}
			if e.Kind == "file" {
				if info, ierr := d.Info(); ierr == nil {
					e.Size = info.Size()
				}
			}
			out.Entries = append(out.Entries, e)
		}
		if d.IsDir() && depthOf(rel) >= depth {
			return fs.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortEntries(out.Entries)
	return out, nil
}

func kindOf(d fs.DirEntry) string {
	switch {
	case d.Type()&fs.ModeSymlink != 0:
		return "symlink"
	case d.IsDir():
		return "dir"
	default:
		return "file"
	}
}

func depthOf(rel string) int {
	if rel == "." {
		return 0
	}
	n := 1
	for _, r := range rel {
		if r == '/' || r == '\\' {
			n++
		}
	}
	return n
}

func sortEntries(es []Entry) {
	sort.Slice(es, func(i, j int) bool {
		if (es[i].Kind == "dir") != (es[j].Kind == "dir") {
			return es[i].Kind == "dir"
		}
		return es[i].Path < es[j].Path
	})
}
