package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func newTestWorkspace(t *testing.T) *Workspace {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "sub", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "sub", "file.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	w, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return w
}

func wantEscape(t *testing.T, w *Workspace, p string) {
	t.Helper()
	got, err := w.Resolve(p)
	if err == nil {
		t.Errorf("Resolve(%q) = %q, want escape error", p, got)
		return
	}
	var ee *EscapeError
	if !errors.As(err, &ee) {
		t.Errorf("Resolve(%q) error = %v, want *EscapeError", p, err)
	}
}

func wantInside(t *testing.T, w *Workspace, p, wantSuffix string) {
	t.Helper()
	got, err := w.Resolve(p)
	if err != nil {
		t.Errorf("Resolve(%q): %v", p, err)
		return
	}
	if wantSuffix != "" && filepath.Base(got) != wantSuffix {
		t.Errorf("Resolve(%q) = %q, want base %q", p, got, wantSuffix)
	}
	if rel, rerr := filepath.Rel(w.Root(), got); rerr != nil || rel == ".." || filepath.IsAbs(rel) {
		t.Errorf("Resolve(%q) = %q is outside root %q", p, got, w.Root())
	}
}

func TestTraversalEscape(t *testing.T) {
	w := newTestWorkspace(t)
	wantEscape(t, w, "../../../etc/passwd")
	wantEscape(t, w, "..")
	wantEscape(t, w, "sub/../../outside")
	wantEscape(t, w, "sub/../..")
}

func TestAbsolutePathEscape(t *testing.T) {
	w := newTestWorkspace(t)
	wantEscape(t, w, "/etc/passwd")
	wantEscape(t, w, "/etc")
	wantEscape(t, w, "/tmp")
}

func TestSymlinkEscape(t *testing.T) {
	w := newTestWorkspace(t)
	root := w.Root()

	// File symlink pointing outside.
	if err := os.Symlink("/etc/passwd", filepath.Join(root, "evil_file")); err != nil {
		t.Fatal(err)
	}
	wantEscape(t, w, "evil_file")

	// Directory symlink pointing outside; traversal through it must be caught.
	if err := os.Symlink("/etc", filepath.Join(root, "evil_dir")); err != nil {
		t.Fatal(err)
	}
	wantEscape(t, w, "evil_dir/passwd")
	wantEscape(t, w, "evil_dir")

	// Symlink chain: inside -> outside -> deeper outside. The link sits in
	// sub/ and points two levels up, out of the workspace root.
	if err := os.Symlink("../..", filepath.Join(root, "sub", "up")); err != nil {
		t.Fatal(err)
	}
	wantEscape(t, w, "sub/up/secret")

	// Non-existing path whose parent symlink points outside.
	if err := os.Symlink("/tmp", filepath.Join(root, "to_tmp")); err != nil {
		t.Fatal(err)
	}
	wantEscape(t, w, "to_tmp/new_file.txt")
}

func TestSymlinkInsideAllowed(t *testing.T) {
	w := newTestWorkspace(t)
	root := w.Root()

	// Symlink to a sibling inside the workspace is fine.
	if err := os.Symlink(filepath.Join(root, "sub", "file.txt"), filepath.Join(root, "alias.txt")); err != nil {
		t.Fatal(err)
	}
	got, err := w.Resolve("alias.txt")
	if err != nil {
		t.Fatalf("Resolve(alias.txt): %v", err)
	}
	if got != filepath.Join(root, "sub", "file.txt") {
		t.Errorf("Resolve(alias.txt) = %q, want resolved target", got)
	}

	// Root-internal directory symlink stays inside.
	if err := os.Symlink(filepath.Join(root, "sub"), filepath.Join(root, "sub_alias")); err != nil {
		t.Fatal(err)
	}
	wantInside(t, w, "sub_alias/file.txt", "file.txt")
}

func TestValidPaths(t *testing.T) {
	w := newTestWorkspace(t)
	root := w.Root()

	wantInside(t, w, "", "")
	if got, err := w.Resolve(""); err != nil || got != root {
		t.Errorf("Resolve(\"\") = %q, %v; want root", got, err)
	}
	wantInside(t, w, ".", "")
	wantInside(t, w, "sub/file.txt", "file.txt")
	wantInside(t, w, "sub/deep/../file.txt", "file.txt") // stays inside after cleanup
	wantInside(t, w, filepath.Join(root, "sub", "file.txt"), "file.txt")

	// Non-existing paths inside the workspace resolve to their lexical spot.
	wantInside(t, w, "new.txt", "new.txt")
	wantInside(t, w, "sub/deep/newer.bin", "newer.bin")
	wantInside(t, w, "a/b/c/d.txt", "d.txt") // fully non-existent chain
}

func TestNewRejectsMissingRoot(t *testing.T) {
	if _, err := New(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("expected error for missing root")
	}
	// A file as root must be rejected too.
	f := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := New(f); err == nil {
		t.Fatal("expected error for file as root")
	}
}

func TestNewResolvesRootSymlink(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	w, err := New(link)
	if err != nil {
		t.Fatalf("New(symlink root): %v", err)
	}
	if w.Root() != real {
		t.Errorf("Root() = %q, want canonical %q", w.Root(), real)
	}
}

func TestResolveParent(t *testing.T) {
	w := newTestWorkspace(t)
	root := w.Root()

	// A symlink's own location is returned unresolved, so delete can unlink
	// the link instead of its target.
	if err := os.Symlink("/etc", filepath.Join(root, "evil_dir")); err != nil {
		t.Fatal(err)
	}
	got, err := w.ResolveParent("evil_dir")
	if err != nil {
		t.Fatalf("ResolveParent: %v", err)
	}
	if got != filepath.Join(root, "evil_dir") {
		t.Errorf("ResolveParent = %q, want the link itself %q", got, filepath.Join(root, "evil_dir"))
	}

	// Regular files resolve the same as Resolve for the final component.
	got, err = w.ResolveParent("sub/file.txt")
	if err != nil {
		t.Fatalf("ResolveParent: %v", err)
	}
	if got != filepath.Join(root, "sub", "file.txt") {
		t.Errorf("ResolveParent = %q", got)
	}

	// Escapes are still rejected.
	if _, err := w.ResolveParent("../../outside"); err == nil {
		t.Error("expected escape error")
	}
}

func TestRel(t *testing.T) {
	w := newTestWorkspace(t)
	got := w.Rel(filepath.Join(w.Root(), "sub", "file.txt"))
	if got != filepath.Join("sub", "file.txt") {
		t.Errorf("Rel = %q", got)
	}
}
