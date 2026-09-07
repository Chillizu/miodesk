package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteCreateAndOverwrite(t *testing.T) {
	ws, root := newWS(t)

	// Simple create.
	out, err := Write(context.Background(), ws, WriteInput{Path: "new.txt", Content: "hi\n"})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if !out.Created || out.Bytes != 3 {
		t.Errorf("output = %+v", out)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "new.txt")); string(data) != "hi\n" {
		t.Errorf("content = %q", data)
	}

	// Overwrite without create_dirs.
	out, err = Write(context.Background(), ws, WriteInput{Path: "new.txt", Content: "updated"})
	if err != nil {
		t.Fatalf("Write overwrite: %v", err)
	}
	if out.Created {
		t.Error("overwrite must report created=false")
	}
}

func TestWriteRequiresCreateDirsForMissingParents(t *testing.T) {
	ws, root := newWS(t)
	if _, err := Write(context.Background(), ws, WriteInput{Path: "deep/dir/new.txt", Content: "x"}); err == nil {
		t.Fatal("missing parents without create_dirs should error")
	}
	if _, err := os.Stat(filepath.Join(root, "deep")); !os.IsNotExist(err) {
		t.Error("no directory may be created without create_dirs")
	}
	if _, err := Write(context.Background(), ws, WriteInput{Path: "deep/dir/new.txt", Content: "x", CreateDirs: true}); err != nil {
		t.Fatalf("Write with create_dirs: %v", err)
	}
}

func TestWriteRejectsDirAndEscape(t *testing.T) {
	ws, root := newWS(t)
	if err := os.Mkdir(filepath.Join(root, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(context.Background(), ws, WriteInput{Path: "d", Content: "x"}); err == nil {
		t.Error("writing over a directory must error")
	}
	if _, err := Write(context.Background(), ws, WriteInput{Path: "../evil.txt", Content: "x"}); err == nil {
		t.Error("escape must be rejected")
	}
}

func TestDeleteFileAndSymlink(t *testing.T) {
	ws, root := newWS(t)
	writeFile(t, root, "a.txt", "x")

	out, err := Delete(context.Background(), ws, DeleteInput{Path: "a.txt"})
	if err != nil || out.Target != "file" {
		t.Fatalf("Delete file = %+v, %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(root, "a.txt")); !os.IsNotExist(err) {
		t.Error("file should be gone")
	}

	// Deleting a symlink unlinks the link, not the target.
	writeFile(t, root, "sub/target.txt", "target")
	if err := os.Symlink(filepath.Join(root, "sub"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	out, err = Delete(context.Background(), ws, DeleteInput{Path: "link"})
	if err != nil || out.Target != "symlink" {
		t.Fatalf("Delete symlink = %+v, %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(root, "sub")); err != nil {
		t.Error("symlink target must survive")
	}
	if _, err := Delete(context.Background(), ws, DeleteInput{Path: "missing"}); err == nil {
		t.Error("missing path should error")
	}
}

func TestDeleteDirectoryRequiresRecursive(t *testing.T) {
	ws, root := newWS(t)
	writeFile(t, root, "d/x.txt", "x")

	if _, err := Delete(context.Background(), ws, DeleteInput{Path: "d"}); err == nil {
		t.Fatal("non-empty dir without recursive must error")
	} else if !strings.Contains(err.Error(), "recursive=true") {
		t.Errorf("error should explain the flag: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "d")); err != nil {
		t.Error("directory must survive failed delete")
	}

	out, err := Delete(context.Background(), ws, DeleteInput{Path: "d", Recursive: true})
	if err != nil || out.Target != "dir" {
		t.Fatalf("recursive delete = %+v, %v", out, err)
	}

	// Empty directories delete without the flag.
	if err := os.Mkdir(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Delete(context.Background(), ws, DeleteInput{Path: "empty"}); err != nil {
		t.Fatalf("empty dir delete: %v", err)
	}

	// The workspace root is never deletable.
	if _, err := Delete(context.Background(), ws, DeleteInput{Path: ".", Recursive: true}); err == nil {
		t.Error("workspace root must be refused")
	}
}

func TestEditExactReplace(t *testing.T) {
	ws, root := newWS(t)
	writeFile(t, root, "f.txt", "alpha\nbeta\ngamma\n")

	out, err := Edit(context.Background(), ws, EditInput{Operations: []EditOperation{
		{Path: "f.txt", Old: "beta", New: "BETA"},
	}})
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if out.Applied != 1 || len(out.Files) != 1 {
		t.Fatalf("output = %+v", out)
	}
	data, _ := os.ReadFile(filepath.Join(root, "f.txt"))
	if string(data) != "alpha\nBETA\ngamma\n" {
		t.Errorf("content = %q", data)
	}
	if len(out.Files[0].Diff) == 0 {
		t.Error("edit result should include a diff")
	}
}

func TestEditAmbiguityRequiresReplaceAll(t *testing.T) {
	ws, root := newWS(t)
	writeFile(t, root, "f.txt", "x\nx\nx\n")

	if _, err := Edit(context.Background(), ws, EditInput{Operations: []EditOperation{
		{Path: "f.txt", Old: "x", New: "y"},
	}}); err == nil {
		t.Fatal("ambiguous replace must fail")
	}
	if data, _ := os.ReadFile(filepath.Join(root, "f.txt")); string(data) != "x\nx\nx\n" {
		t.Error("failed edit must not write anything")
	}

	if _, err := Edit(context.Background(), ws, EditInput{Operations: []EditOperation{
		{Path: "f.txt", Old: "x", New: "y", ReplaceAll: true},
	}}); err != nil {
		t.Fatalf("replace_all: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "f.txt")); string(data) != "y\ny\ny\n" {
		t.Errorf("content = %q", data)
	}
}

func TestEditRangeWithGuard(t *testing.T) {
	ws, root := newWS(t)
	writeFile(t, root, "f.txt", "one\ntwo\nthree\nfour\n")

	// Wrong guard rejects.
	if _, err := Edit(context.Background(), ws, EditInput{Operations: []EditOperation{
		{Path: "f.txt", StartLine: 2, EndLine: 3, Expected: "wrong", New: "TWO\nTHREE"},
	}}); err == nil {
		t.Fatal("wrong guard must fail")
	}
	// Right guard applies.
	if _, err := Edit(context.Background(), ws, EditInput{Operations: []EditOperation{
		{Path: "f.txt", StartLine: 2, EndLine: 3, Expected: "two\nthree", New: "TWO\nTHREE"},
	}}); err != nil {
		t.Fatalf("guarded range edit: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "f.txt")); string(data) != "one\nTWO\nTHREE\nfour\n" {
		t.Errorf("content = %q", data)
	}
}

func TestEditBatchIsAtomic(t *testing.T) {
	ws, root := newWS(t)
	writeFile(t, root, "a.txt", "aaa\n")
	writeFile(t, root, "b.txt", "bbb\n")

	// First op valid, second invalid: neither file may change.
	_, err := Edit(context.Background(), ws, EditInput{Operations: []EditOperation{
		{Path: "a.txt", Old: "aaa", New: "AAA"},
		{Path: "b.txt", Old: "missing-text", New: "BBB"},
	}})
	if err == nil {
		t.Fatal("batch with a bad op must fail")
	}
	for _, f := range []string{"a.txt", "b.txt"} {
		data, _ := os.ReadFile(filepath.Join(root, f))
		if strings.HasPrefix(string(data), "AAA") || strings.Contains(string(data), "BBB") {
			t.Errorf("%s was modified by a failed batch: %q", f, data)
		}
	}
}

func TestEditErrors(t *testing.T) {
	ws, root := newWS(t)
	writeFile(t, root, "f.txt", "one\ntwo\n")
	if _, err := Edit(context.Background(), ws, EditInput{}); err == nil {
		t.Error("empty batch must error")
	}
	if _, err := Edit(context.Background(), ws, EditInput{Operations: []EditOperation{
		{Path: "missing.txt", Old: "a", New: "b"},
	}}); err == nil {
		t.Error("missing file must error")
	}
	if _, err := Edit(context.Background(), ws, EditInput{Operations: []EditOperation{
		{Path: "f.txt", StartLine: 5, EndLine: 9, New: "x"},
	}}); err == nil {
		t.Error("out-of-range lines must error")
	}
	if _, err := Edit(context.Background(), ws, EditInput{Operations: []EditOperation{
		{Path: "f.txt", Old: "one", New: "1", StartLine: 1, EndLine: 1},
	}}); err == nil {
		t.Error("mixed op kinds must error")
	}
}
