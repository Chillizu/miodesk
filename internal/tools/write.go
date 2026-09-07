package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"miodesk/internal/workspace"
)

// MaxWriteBytes caps one write call.
const MaxWriteBytes = 8 << 20

// WriteInput creates or overwrites one file inside the workspace.
type WriteInput struct {
	Path       string `json:"path" jsonschema:"file path relative to the workspace root, or absolute inside it"`
	Content    string `json:"content"`
	CreateDirs bool   `json:"create_dirs,omitempty" jsonschema:"create missing parent directories (default false)"`
}

type WriteOutput struct {
	Kind    string `json:"kind"` // "write"
	Path    string `json:"path"`
	Bytes   int    `json:"bytes"`
	Created bool   `json:"created"`
}

// Write creates or replaces a file. Missing parent directories are only
// created when CreateDirs is set — writing never silently builds trees.
func Write(ctx context.Context, ws *workspace.Workspace, in WriteInput) (*WriteOutput, error) {
	if len(in.Content) > MaxWriteBytes {
		return nil, fmt.Errorf("write: content is %d bytes, cap is %d", len(in.Content), MaxWriteBytes)
	}
	path, err := ws.Resolve(in.Path)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(path); err == nil && fi.IsDir() {
		return nil, fmt.Errorf("write: %s is a directory", ws.Rel(path))
	}

	existed := true
	if _, err := os.Stat(path); os.IsNotExist(err) {
		existed = false
	}
	if !existed && in.CreateDirs {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("write: create directories: %w", err)
		}
	}
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		return nil, fmt.Errorf("write: parent directory missing (pass create_dirs=true): %s", ws.Rel(filepath.Dir(path)))
	}

	if err := writeFileAtomic(path, []byte(in.Content)); err != nil {
		return nil, err
	}
	return &WriteOutput{Kind: "write", Path: ws.Rel(path), Bytes: len(in.Content), Created: !existed}, nil
}

// writeFileAtomic writes via a temp file + rename in the same directory so a
// crash mid-write never leaves a half-written target.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		// Replacing an existing file should not unexpectedly remove its
		// executable/read-only mode bits. The rename still requires a writable
		// parent, just like any other atomic replacement.
		mode = fi.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, ".miodesk-write-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
