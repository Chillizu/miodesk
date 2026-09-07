// Package workspace implements miodesk's security boundary: every file path a
// tool touches must resolve to a location inside the workspace root.
//
// Validation is based on canonical, symlink-resolved paths — never on string
// prefixes — so that escapes via ../, absolute paths, or symlinks pointing
// outside the workspace are all rejected.
package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Workspace is a validated, symlink-resolved workspace root.
type Workspace struct {
	root string
}

// New validates that root exists, is a directory, and canonicalizes it.
func New(root string) (*Workspace, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("workspace root %q: %w", root, err)
	}
	fi, err := os.Stat(resolved)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("workspace root %q is not a directory", root)
	}
	return &Workspace{root: resolved}, nil
}

// Root returns the canonical workspace root path.
func (w *Workspace) Root() string { return w.root }

// Resolve maps p — absolute, or relative to the workspace root — to its
// canonical location inside the workspace. Symlinks in every existing
// component are resolved; the result must stay inside the root.
//
// A path may name a not-yet-existing file (needed for future write tools) as
// long as none of its existing ancestors escape the workspace.
func (w *Workspace) Resolve(p string) (string, error) {
	if p == "" {
		return w.root, nil
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(w.root, p)
	}
	p = filepath.Clean(p)
	resolved, err := resolveExisting(p)
	if err != nil {
		return "", err
	}
	if !w.contains(resolved) {
		return "", &EscapeError{Path: p, Resolved: resolved}
	}
	return resolved, nil
}

// ResolveParent maps p to a path whose parent components are fully
// symlink-resolved, with the final component kept as-is. This is the entry
// point for operations that must act on the final component itself — deleting
// a symlink without following it — while still guaranteeing the location is
// inside the workspace.
func (w *Workspace) ResolveParent(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("path must not be empty")
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(w.root, p)
	}
	p = filepath.Clean(p)
	parent, base := filepath.Split(p)
	resolvedParent, err := resolveExisting(filepath.Clean(parent))
	if err != nil {
		return "", err
	}
	joined := filepath.Join(resolvedParent, base)
	if !w.contains(joined) {
		return "", &EscapeError{Path: p, Resolved: joined}
	}
	return joined, nil
}

// Rel returns p as a workspace-relative display path. The path must have been
// produced by Resolve.
func (w *Workspace) Rel(p string) string {
	rel, err := filepath.Rel(w.root, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return p
	}
	return rel
}

func (w *Workspace) contains(resolved string) bool {
	rel, err := filepath.Rel(w.root, resolved)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// resolveExisting resolves symlinks of all components of p that currently
// exist and appends the non-existing tail lexically. Appending is safe because
// the deepest existing ancestor has already been fully resolved — including
// any symlinks in it.
func resolveExisting(p string) (string, error) {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r, nil
	}
	parent := filepath.Clean(filepath.Dir(p))
	if parent == p {
		// Filesystem root reached without resolution; EvalSymlinks on the
		// root itself would have succeeded on any sane system.
		return p, nil
	}
	rp, err := resolveExisting(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(rp, filepath.Base(p)), nil
}

// EscapeError reports a path that resolves outside the workspace.
type EscapeError struct {
	Path     string // the path as requested
	Resolved string // where it actually points
}

func (e *EscapeError) Error() string {
	return fmt.Sprintf("path %q escapes the workspace (resolves to %q)", e.Path, e.Resolved)
}
