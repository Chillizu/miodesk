package tools

import (
	"context"
	"fmt"
	"os"

	"github.com/Chillizu/miodesk/internal/workspace"
)

// DeleteInput removes one file, symlink, or directory inside the workspace.
type DeleteInput struct {
	Path      string `json:"path" jsonschema:"path relative to the workspace root, or absolute inside it"`
	Recursive bool   `json:"recursive,omitempty" jsonschema:"required to delete a non-empty directory"`
}

type DeleteOutput struct {
	Kind string `json:"kind"` // "delete"
	Path string `json:"path"`
	// Target is what was removed: file | dir | symlink.
	Target string `json:"target"`
}

// Delete removes the final path component itself — a symlink is unlinked, not
// followed. Non-empty directories require Recursive=true; the workspace root
// is never deletable.
func Delete(ctx context.Context, ws *workspace.Workspace, in DeleteInput) (*DeleteOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := ws.ResolveParent(in.Path)
	if err != nil {
		return nil, err
	}
	if path == ws.Root() {
		return nil, fmt.Errorf("delete: refusing to delete the workspace root")
	}
	fi, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("delete: not found: %s", ws.Rel(path))
		}
		return nil, err
	}

	kind := "file"
	if fi.Mode()&os.ModeSymlink != 0 {
		kind = "symlink"
	} else if fi.IsDir() {
		kind = "dir"
		entries, err := os.ReadDir(path)
		if err != nil {
			return nil, err
		}
		if len(entries) > 0 && !in.Recursive {
			return nil, fmt.Errorf("delete: %s is a directory with %d entries (pass recursive=true)", ws.Rel(path), len(entries))
		}
	}

	if kind == "dir" {
		if err := os.RemoveAll(path); err != nil {
			return nil, err
		}
	} else if err := os.Remove(path); err != nil {
		return nil, err
	}
	return &DeleteOutput{Kind: "delete", Path: ws.Rel(path), Target: kind}, nil
}
