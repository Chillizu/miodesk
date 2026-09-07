package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"miodesk/internal/workspace"
)

func newWS(t *testing.T) (*workspace.Workspace, string) {
	t.Helper()
	root := t.TempDir()
	ws, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	return ws, root
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadBasic(t *testing.T) {
	ws, root := newWS(t)
	writeFile(t, root, "notes/a.txt", "l1\nl2\nl3\nl4\n")

	out, err := Read(context.Background(), ws, ReadInput{Path: "notes/a.txt"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if out.Content != "l1\nl2\nl3\nl4\n" || out.Lines != 4 || out.Truncated {
		t.Errorf("basic read = %+v", out)
	}
	if out.Path != filepath.Join("notes", "a.txt") {
		t.Errorf("path = %q", out.Path)
	}
	if out.Size != 12 {
		t.Errorf("size = %d", out.Size)
	}
}

func TestReadWindow(t *testing.T) {
	ws, root := newWS(t)
	writeFile(t, root, "w.txt", "l1\nl2\nl3\nl4\n")

	out, err := Read(context.Background(), ws, ReadInput{Path: "w.txt", Offset: 2, Limit: 2})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if out.Content != "l2\nl3\n" || out.Lines != 2 {
		t.Errorf("window = %+v", out)
	}
	// More lines exist beyond the requested window.
	if !out.Truncated {
		t.Error("truncated should be true when lines remain beyond the window")
	}

	// Window ending exactly at EOF is not truncated.
	out, err = Read(context.Background(), ws, ReadInput{Path: "w.txt", Offset: 3, Limit: 2})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if out.Content != "l3\nl4\n" || out.Truncated {
		t.Errorf("eof window = %+v", out)
	}

	// Offset past EOF yields empty content.
	out, err = Read(context.Background(), ws, ReadInput{Path: "w.txt", Offset: 99})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if out.Content != "" || out.Lines != 0 || out.Truncated {
		t.Errorf("past-eof = %+v", out)
	}
}

func TestReadByteCap(t *testing.T) {
	ws, root := newWS(t)
	big := strings.Repeat("x", MaxReadBytes+4096)
	writeFile(t, root, "big.txt", big)

	out, err := Read(context.Background(), ws, ReadInput{Path: "big.txt"})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !out.Truncated {
		t.Error("oversized file must be reported truncated")
	}
	if int64(len(out.Content)) != MaxReadBytes {
		t.Errorf("content len = %d, want cap %d", len(out.Content), MaxReadBytes)
	}
}

func TestReadRejectsBinary(t *testing.T) {
	ws, root := newWS(t)
	p := filepath.Join(root, "bin.dat")
	if err := os.WriteFile(p, append([]byte("ok\x00\x01"), make([]byte, 64)...), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(context.Background(), ws, ReadInput{Path: "bin.dat"}); err == nil {
		t.Fatal("binary file should be rejected")
	}
}

func TestReadErrors(t *testing.T) {
	ws, root := newWS(t)
	if _, err := Read(context.Background(), ws, ReadInput{Path: "nope.txt"}); err == nil {
		t.Error("missing file should error")
	}
	if err := os.Mkdir(filepath.Join(root, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(context.Background(), ws, ReadInput{Path: "d"}); err == nil {
		t.Error("directory read should error")
	}
	if _, err := Read(context.Background(), ws, ReadInput{Path: "../../etc/passwd"}); err == nil {
		t.Error("escape must be rejected")
	}
}

func writeTree(t *testing.T, root string) {
	t.Helper()
	writeFile(t, root, "a.txt", "aaaaa")
	writeFile(t, root, "sub/b.txt", "b")
	writeFile(t, root, "sub/deep/c.txt", "c")
	writeFile(t, root, ".hidden/h.txt", "h")
	if err := os.Symlink(filepath.Join(root, "sub"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
}

func TestListDepthOne(t *testing.T) {
	ws, root := newWS(t)
	writeTree(t, root)

	out, err := List(context.Background(), ws, ListInput{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if out.Truncated || len(out.Entries) != 4 {
		t.Fatalf("depth-1 entries = %+v", out)
	}
	if out.Entries[0].Kind != "dir" {
		t.Errorf("entries should be dirs-first: %+v", out.Entries)
	}
	kinds := map[string]string{}
	for _, e := range out.Entries {
		kinds[e.Name] = e.Kind
	}
	if kinds["a.txt"] != "file" || kinds["link"] != "symlink" {
		t.Errorf("kinds = %v", kinds)
	}
}

func TestListDepthTwo(t *testing.T) {
	ws, root := newWS(t)
	writeTree(t, root)

	out, err := List(context.Background(), ws, ListInput{Depth: 2})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	paths := map[string]bool{}
	for _, e := range out.Entries {
		paths[e.Path] = true
	}
	for _, want := range []string{"sub", filepath.Join("sub", "b.txt"), filepath.Join("sub", "deep"), filepath.Join(".hidden", "h.txt")} {
		if !paths[want] {
			t.Errorf("missing entry %q in %v", want, paths)
		}
	}
	// depth 2 must not descend into sub/deep itself
	if paths[filepath.Join("sub", "deep", "c.txt")] {
		t.Error("should not descend below depth 2")
	}
}

func TestListLimit(t *testing.T) {
	ws, root := newWS(t)
	writeTree(t, root)

	out, err := List(context.Background(), ws, ListInput{Limit: 3})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(out.Entries) != 3 || !out.Truncated {
		t.Errorf("limited list = %+v", out)
	}
}

func TestListErrors(t *testing.T) {
	ws, root := newWS(t)
	writeFile(t, root, "f.txt", "x")
	if _, err := List(context.Background(), ws, ListInput{Path: "f.txt"}); err == nil {
		t.Error("listing a file should error")
	}
	if _, err := List(context.Background(), ws, ListInput{Path: "../outside"}); err == nil {
		t.Error("escape must be rejected")
	}
}

func TestSearchBuiltin(t *testing.T) {
	rgDisabledFor = true
	defer func() { rgDisabledFor = false }()

	ws, root := newWS(t)
	writeFile(t, root, "one.txt", "alpha beta\nGAMMA\nlast alpha\n")
	writeFile(t, root, "two.txt", "alpha here\n")
	writeFile(t, root, ".hidden/h.txt", "alpha\n")
	if err := os.WriteFile(filepath.Join(root, "bin.dat"), []byte("al\x00pha"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := Search(context.Background(), ws, SearchInput{Query: "alpha"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if out.Engine != "builtin" {
		t.Errorf("engine = %q, want builtin", out.Engine)
	}
	if len(out.Matches) != 3 {
		t.Fatalf("matches = %+v", out.Matches)
	}
	if out.Matches[0].File != "one.txt" || out.Matches[0].Line != 1 {
		t.Errorf("first match = %+v", out.Matches[0])
	}

	// Case-insensitive by default.
	out, err = Search(context.Background(), ws, SearchInput{Query: "gamma"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(out.Matches) != 1 || out.Matches[0].Line != 2 {
		t.Errorf("case-insensitive matches = %+v", out.Matches)
	}
	// ...and case-sensitive on demand.
	out, err = Search(context.Background(), ws, SearchInput{Query: "GAMMA", CaseSensitive: true})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(out.Matches) != 1 {
		t.Errorf("case-sensitive matches = %+v", out.Matches)
	}

	// Hidden directories are skipped; binary files produce no bogus matches.
	out, err = Search(context.Background(), ws, SearchInput{Query: "alpha"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, m := range out.Matches {
		if strings.HasPrefix(m.File, ".hidden") || strings.HasSuffix(m.File, "bin.dat") {
			t.Errorf("unexpected match: %+v", m)
		}
	}

	// max_results caps and flags truncation.
	out, err = Search(context.Background(), ws, SearchInput{Query: "alpha", MaxResults: 2})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(out.Matches) != 2 || !out.Truncated {
		t.Errorf("capped search = %+v", out)
	}

	// Empty query is rejected.
	if _, err := Search(context.Background(), ws, SearchInput{Query: "  "}); err == nil {
		t.Error("empty query should error")
	}
}

func TestSearchRipgrep(t *testing.T) {
	if rgPath() == "" {
		t.Skip("ripgrep not installed")
	}
	ws, root := newWS(t)
	writeFile(t, root, "rg.txt", "needle one\nnothing\nneedle two\n")

	out, err := Search(context.Background(), ws, SearchInput{Query: "needle"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if out.Engine != "ripgrep" {
		t.Fatalf("engine = %q, want ripgrep", out.Engine)
	}
	if len(out.Matches) != 2 || out.Matches[1].Line != 3 {
		t.Errorf("matches = %+v", out.Matches)
	}

	out, err = Search(context.Background(), ws, SearchInput{Query: "absent-needle-xyz"})
	if err != nil {
		t.Fatalf("Search no-match: %v", err)
	}
	if len(out.Matches) != 0 || out.Truncated {
		t.Errorf("no-match result = %+v", out)
	}
}

func TestSearchOutputBufferKeepsBoundedPrefix(t *testing.T) {
	var b searchOutputBuffer
	b.max = 5
	if n, err := b.Write([]byte("abcdef")); err != nil || n != 6 {
		t.Fatalf("Write = (%d, %v)", n, err)
	}
	if got := b.data.String(); got != "abcde" || !b.Truncated() {
		t.Errorf("buffer = %q, truncated=%v", got, b.Truncated())
	}
}

func TestSearchRespectsSandbox(t *testing.T) {
	ws, _ := newWS(t)
	if _, err := Search(context.Background(), ws, SearchInput{Query: "x", Path: "../../etc"}); err == nil {
		t.Error("escape must be rejected")
	}
}
