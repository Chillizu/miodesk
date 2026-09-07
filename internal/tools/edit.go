package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"miodesk/internal/workspace"
)

const (
	// MaxEditBytes caps the file size the edit tool will load.
	MaxEditBytes = 2 << 20
	// MaxEditOps bounds one atomic batch.
	MaxEditOps = 100
)

// EditInput is a batch of operations: every operation is validated against
// in-memory copies before anything is written, so a failed validation leaves
// every file untouched. Each file write itself is atomic (temp + rename);
// if the filesystem fails between per-file writes, earlier files may already
// have been updated.
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

// Edit applies exact-replace and guarded range operations atomically. All
// operations are validated against in-memory copies first; only when every
// operation succeeds are the files written (temp + rename per file).
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
	type fileEdit struct {
		path   string // resolved
		old    string
		new    string
		result EditFileResult
	}
	edits := make([]fileEdit, 0, len(order))
	for _, key := range order {
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
		edits = append(edits, fileEdit{
			path: path,
			old:  string(data),
			new:  content,
			result: EditFileResult{
				Path:  ws.Rel(path),
				Bytes: len(content),
				Lines: len(splitLines(content)),
			},
		})
	}

	// Phase 2: everything validated — write. Files whose content did not
	// change are skipped.
	out := &EditOutput{}
	for i := range edits {
		e := &edits[i]
		if e.old == e.new {
			continue
		}
		if err := writeFileAtomic(e.path, []byte(e.new)); err != nil {
			return nil, fmt.Errorf("edit: write %s: %w", ws.Rel(e.path), err)
		}
		e.result.Diff = DiffLines(e.old, e.new)
		out.Files = append(out.Files, e.result)
	}
	out.Kind = "edit"
	out.Applied = len(in.Operations)
	return out, nil
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
