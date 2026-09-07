package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Chillizu/miodesk/internal/workspace"
)

const (
	// MaxEditBytes caps the file size the edit tool will load.
	MaxEditBytes = 2 << 20
	// MaxEditOps bounds one atomic batch.
	MaxEditOps = 100
)

// EditInput is a batch of operations: every operation is validated against
// in-memory copies before anything is written, and the commit phase rolls back
// already-installed files when a later filesystem operation fails.
type EditInput struct {
	Operations []EditOperation `json:"operations" jsonschema:"operations applied atomically; a failed batch leaves every file untouched"`
}

type EditOperation struct {
	Path string `json:"path" jsonschema:"existing file to edit, relative to the workspace root"`
	// Replace operation: replace Old with New.
	Old        string `json:"old,omitempty" jsonschema:"exact text to replace"`
	New        string `json:"new,omitempty" jsonschema:"replacement text"`
	ReplaceAll bool   `json:"replace_all,omitempty" jsonschema:"replace every occurrence of old (default: exactly one)"`
	// Range operation: replace lines StartLine..EndLine (inclusive, 1-based).
	StartLine int    `json:"start_line,omitempty" jsonschema:"range edit: first line to replace (1-based)"`
	EndLine   int    `json:"end_line,omitempty" jsonschema:"range edit: last line to replace (inclusive)"`
	Expected  string `json:"expected,omitempty" jsonschema:"range edit guard: exact current content of the range"`
}

type EditFileResult struct {
	Path  string      `json:"path"`
	Bytes int         `json:"bytes"`
	Lines int         `json:"lines"`
	Diff  []DiffGroup `json:"diff"`
}

type EditOutput struct {
	Kind    string           `json:"kind"` // "edit"
	Applied int              `json:"applied"`
	Files   []EditFileResult `json:"files"`
}

// Edit applies exact-replace and guarded range operations as one transaction.
// All operations are validated against in-memory copies first; the commit
// stages every changed file and restores prior contents if a later rename
// fails. As with any filesystem transaction without a journal, a sudden power
// loss during the commit cannot provide database-level crash atomicity.
func Edit(ctx context.Context, ws *workspace.Workspace, in EditInput) (*EditOutput, error) {
	if len(in.Operations) == 0 {
		return nil, fmt.Errorf("edit: no operations")
	}
	if len(in.Operations) > MaxEditOps {
		return nil, fmt.Errorf("edit: %d operations exceeds the batch cap of %d", len(in.Operations), MaxEditOps)
	}

	// ops per file, in request order.
	byPath := map[string][]EditOperation{}
	order := []string{}
	for i, op := range in.Operations {
		if op.Path == "" {
			return nil, fmt.Errorf("edit: operation %d has no path", i)
		}
		if _, seen := byPath[op.Path]; !seen {
			order = append(order, op.Path)
		}
		byPath[op.Path] = append(byPath[op.Path], op)
	}

	// Phase 1: validate and compute every new content in memory.
	edits := make([]preparedEdit, 0, len(order))
	for _, key := range order {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		path, err := ws.Resolve(key)
		if err != nil {
			return nil, err
		}
		fi, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("edit: file not found: %s", ws.Rel(path))
			}
			return nil, err
		}
		if fi.IsDir() {
			return nil, fmt.Errorf("edit: %s is a directory", ws.Rel(path))
		}
		if fi.Size() > MaxEditBytes {
			return nil, fmt.Errorf("edit: %s is %d bytes, cap is %d", ws.Rel(path), fi.Size(), MaxEditBytes)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		content := string(data)
		for _, op := range byPath[key] {
			content, err = applyOperation(ws, path, content, op)
			if err != nil {
				return nil, err
			}
		}
		edits = append(edits, preparedEdit{
			path: path,
			old:  string(data),
			new:  content,
			mode: fi.Mode().Perm(),
			result: EditFileResult{
				Path:  ws.Rel(path),
				Bytes: len(content),
				Lines: len(splitLines(content)),
			},
		})
	}

	// Phase 2: everything validated — commit all changed files together.
	if err := commitEdits(ctx, edits); err != nil {
		return nil, err
	}
	out := &EditOutput{}
	for i := range edits {
		e := &edits[i]
		if e.old == e.new {
			continue
		}
		e.result.Diff = DiffLines(e.old, e.new)
		out.Files = append(out.Files, e.result)
	}
	out.Kind = "edit"
	out.Applied = len(in.Operations)
	return out, nil
}

type preparedEdit struct {
	path   string // resolved
	old    string
	new    string
	mode   os.FileMode
	result EditFileResult
}

type stagedEdit struct {
	preparedEdit
	staged    string
	backup    string
	backedUp  bool
	installed bool
}

// commitEdits provides process-level rollback across multiple files. Every
// staged file lives beside its target, so each rename remains on one
// filesystem and is atomic on the platforms miodesk supports.
func commitEdits(ctx context.Context, edits []preparedEdit) error {
	staged := make([]stagedEdit, 0, len(edits))
	cleanup := func() {
		for i := range staged {
			if staged[i].staged != "" {
				_ = os.Remove(staged[i].staged)
			}
			if staged[i].backup != "" && !staged[i].backedUp {
				_ = os.Remove(staged[i].backup)
			}
		}
	}

	for _, edit := range edits {
		if edit.old == edit.new {
			continue
		}
		if err := ctx.Err(); err != nil {
			cleanup()
			return err
		}
		stage, err := stageEditFile(edit.path, []byte(edit.new), edit.mode)
		if err != nil {
			cleanup()
			return fmt.Errorf("edit: stage %s: %w", edit.path, err)
		}
		staged = append(staged, stagedEdit{preparedEdit: edit, staged: stage})
	}

	rollback := func() error {
		var firstErr error
		for i := len(staged) - 1; i >= 0; i-- {
			e := &staged[i]
			if e.installed {
				if err := os.Remove(e.path); err != nil && !os.IsNotExist(err) && firstErr == nil {
					firstErr = err
				}
			}
			if e.backedUp {
				if err := os.Rename(e.backup, e.path); err != nil && firstErr == nil {
					firstErr = err
				}
			}
		}
		return firstErr
	}

	for i := range staged {
		if err := ctx.Err(); err != nil {
			rollbackErr := rollback()
			cleanup()
			if rollbackErr != nil {
				return fmt.Errorf("edit: commit cancelled and rollback failed: %w (original: %v)", rollbackErr, err)
			}
			return err
		}

		e := &staged[i]
		current, err := os.Lstat(e.path)
		if err != nil {
			rollbackErr := rollback()
			cleanup()
			if rollbackErr != nil {
				return fmt.Errorf("edit: target disappeared and rollback failed: %w", rollbackErr)
			}
			return fmt.Errorf("edit: target changed before commit: %w", err)
		}
		if !current.Mode().IsRegular() {
			rollbackErr := rollback()
			cleanup()
			if rollbackErr != nil {
				return fmt.Errorf("edit: target changed type and rollback failed: %w", rollbackErr)
			}
			return fmt.Errorf("edit: target changed type before commit: %s", e.path)
		}
		data, err := os.ReadFile(e.path)
		if err != nil || string(data) != e.old {
			rollbackErr := rollback()
			cleanup()
			if rollbackErr != nil {
				return fmt.Errorf("edit: target changed and rollback failed: %w", rollbackErr)
			}
			if err != nil {
				return fmt.Errorf("edit: reread target before commit: %w", err)
			}
			return fmt.Errorf("edit: target changed before commit: %s", e.path)
		}

		backup, err := makeEditBackup(e.path)
		if err != nil {
			rollbackErr := rollback()
			cleanup()
			if rollbackErr != nil {
				return fmt.Errorf("edit: prepare rollback and rollback failed: %w", rollbackErr)
			}
			return fmt.Errorf("edit: prepare rollback for %s: %w", e.path, err)
		}
		e.backup = backup
		if err := os.Rename(e.path, e.backup); err != nil {
			rollbackErr := rollback()
			cleanup()
			if rollbackErr != nil {
				return fmt.Errorf("edit: backup %s and rollback failed: %w", e.path, rollbackErr)
			}
			return fmt.Errorf("edit: backup %s: %w", e.path, err)
		}
		e.backedUp = true
		if err := os.Rename(e.staged, e.path); err != nil {
			rollbackErr := rollback()
			cleanup()
			if rollbackErr != nil {
				return fmt.Errorf("edit: install %s and rollback failed: %w", e.path, rollbackErr)
			}
			return fmt.Errorf("edit: install %s: %w", e.path, err)
		}
		e.staged = ""
		e.installed = true
	}

	for i := range staged {
		if staged[i].backup != "" {
			_ = os.Remove(staged[i].backup)
		}
	}
	cleanup()
	return nil
}

func stageEditFile(path string, data []byte, mode os.FileMode) (string, error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".miodesk-edit-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	remove := true
	defer func() {
		if remove {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	remove = false
	return tmpName, nil
}

func makeEditBackup(path string) (string, error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".miodesk-edit-backup-*")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	if err := os.Remove(name); err != nil {
		return "", err
	}
	return name, nil
}

func applyOperation(ws *workspace.Workspace, path, content string, op EditOperation) (string, error) {
	hasRange := op.StartLine != 0 || op.EndLine != 0
	hasReplace := op.Old != ""
	switch {
	case hasRange && hasReplace:
		return "", fmt.Errorf("edit: %s: set either old/new or start_line/end_line, not both", ws.Rel(path))
	case hasReplace:
		return applyReplace(ws, path, content, op)
	case hasRange:
		return applyRange(ws, path, content, op)
	default:
		return "", fmt.Errorf("edit: %s: operation needs old/new or start_line/end_line", ws.Rel(path))
	}
}

func applyReplace(ws *workspace.Workspace, path, content string, op EditOperation) (string, error) {
	count := strings.Count(content, op.Old)
	if count == 0 {
		return "", fmt.Errorf("edit: %s: old text not found (it must match exactly, including whitespace)", ws.Rel(path))
	}
	if count > 1 && !op.ReplaceAll {
		return "", fmt.Errorf("edit: %s: old text matches %d places; pass replace_all=true or provide a longer unique snippet", ws.Rel(path), count)
	}
	if !op.ReplaceAll {
		return strings.Replace(content, op.Old, op.New, 1), nil
	}
	return strings.ReplaceAll(content, op.Old, op.New), nil
}

func applyRange(ws *workspace.Workspace, path, content string, op EditOperation) (string, error) {
	lines := splitLines(content)
	if op.StartLine < 1 || op.EndLine < op.StartLine || op.EndLine > len(lines) {
		return "", fmt.Errorf("edit: %s: range %d-%d is outside the file (%d lines)", ws.Rel(path), op.StartLine, op.EndLine, len(lines))
	}
	if op.Expected != "" {
		current := strings.Join(lines[op.StartLine-1:op.EndLine], "\n")
		if current != op.Expected {
			return "", fmt.Errorf("edit: %s: guarded range %d-%d does not match expected content", ws.Rel(path), op.StartLine, op.EndLine)
		}
	}
	replacement := splitLines(op.New)
	// strings.Split("", "\n") returns [""]; a range replaced with empty text
	// should delete the range, not insert one empty line.
	if op.New == "" {
		replacement = nil
	}
	newLines := append([]string{}, lines[:op.StartLine-1]...)
	newLines = append(newLines, replacement...)
	newLines = append(newLines, lines[op.EndLine:]...)
	return strings.Join(newLines, "\n") + "\n", nil
}
