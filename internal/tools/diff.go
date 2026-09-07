package tools

import (
	"fmt"
	"strings"
)

// DiffLine is one line of a generated diff. Old/New are 1-based line numbers
// in the old/new file; 0 means the line does not exist on that side.
type DiffLine struct {
	Type string `json:"type"` // add | del | ctx
	Old  int    `json:"old,omitempty"`
	New  int    `json:"new,omitempty"`
	Text string `json:"text"`
}

// DiffGroup is one contiguous change group ("hunk") with context padding.
type DiffGroup struct {
	Header string     `json:"header"`
	Lines  []DiffLine `json:"lines"`
}

const (
	diffContext  = 3
	diffMergeGap = 2 * diffContext
)

// diffMaxCells bounds the LCS dynamic-programming table; larger middles fall
// back to a single coarse replace group instead of exploding memory. A var
// only so tests can shrink it.
var diffMaxCells = 4 << 20

// DiffLines computes change groups between two file contents. Identical
// inputs yield no groups.
func DiffLines(oldText, newText string) []DiffGroup {
	oldLines := splitLines(oldText)
	newLines := splitLines(newText)
	if equalLines(oldLines, newLines) {
		return nil
	}
	ops := diffOps(oldLines, newLines)
	if len(ops) == 0 {
		return nil
	}
	return buildGroups(oldLines, newLines, ops)
}

type diffOp struct {
	kind byte // 'c' keep, 'd' delete, 'a' add
	old  int  // index into oldLines for 'c'/'d'
	new  int  // index into newLines for 'a'
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// diffOps computes the edit script: trim the common prefix/suffix, then run
// LCS on the middle when it fits the cell budget.
func diffOps(a, b []string) []diffOp {
	prefix := 0
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(a)-prefix && suffix < len(b)-prefix &&
		a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}

	var ops []diffOp
	for i := 0; i < prefix; i++ {
		ops = append(ops, diffOp{kind: 'c', old: i, new: i})
	}
	ops = append(ops, midOps(a[prefix:len(a)-suffix], b[prefix:len(b)-suffix], prefix)...)
	for i := len(a) - suffix; i < len(a); i++ {
		ops = append(ops, diffOp{kind: 'c', old: i, new: i - len(a) + len(b)})
	}
	return ops
}

func midOps(midA, midB []string, prefix int) []diffOp {
	switch {
	case len(midA) == 0 && len(midB) == 0:
		return nil
	case len(midA) == 0:
		ops := make([]diffOp, 0, len(midB))
		for j := range midB {
			ops = append(ops, diffOp{kind: 'a', new: prefix + j})
		}
		return ops
	case len(midB) == 0:
		ops := make([]diffOp, 0, len(midA))
		for i := range midA {
			ops = append(ops, diffOp{kind: 'd', old: prefix + i})
		}
		return ops
	}
	if cells := (len(midA) + 1) * (len(midB) + 1); cells > diffMaxCells {
		// Coarse fallback: treat the middle as fully replaced.
		ops := make([]diffOp, 0, len(midA)+len(midB))
		for i := range midA {
			ops = append(ops, diffOp{kind: 'd', old: prefix + i})
		}
		for j := range midB {
			ops = append(ops, diffOp{kind: 'a', new: prefix + j})
		}
		return ops
	}
	return lcsOps(midA, midB, prefix)
}

// lcsOps backtracks the LCS table of the middle sections.
func lcsOps(a, b []string, prefix int) []diffOp {
	n, m := len(a), len(b)
	table := make([]int32, (n+1)*(m+1))
	at := func(i, j int) *int32 { return &table[i*(m+1)+j] }
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				*at(i, j) = *at(i+1, j+1) + 1
			} else if *at(i+1, j) >= *at(i, j+1) {
				*at(i, j) = *at(i+1, j)
			} else {
				*at(i, j) = *at(i, j+1)
			}
		}
	}
	ops := make([]diffOp, 0, n+m)
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, diffOp{kind: 'c', old: prefix + i, new: prefix + j})
			i++
			j++
		case *at(i+1, j) >= *at(i, j+1):
			ops = append(ops, diffOp{kind: 'd', old: prefix + i})
			i++
		default:
			ops = append(ops, diffOp{kind: 'a', new: prefix + j})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{kind: 'd', old: prefix + i})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{kind: 'a', new: prefix + j})
	}
	return ops
}

type diffEntry struct {
	line     DiffLine
	isChange bool
	oldSnap  int // 1-based old-file position before this op
	newSnap  int
}

// buildGroups turns the op script into change groups padded with context.
// Groups merge when separated by at most diffMergeGap context lines, so the
// viewer shows few, meaningful hunks instead of one group per line.
func buildGroups(oldLines, newLines []string, ops []diffOp) []DiffGroup {
	entries := make([]diffEntry, 0, len(ops))
	o, n := 0, 0
	for _, op := range ops {
		e := diffEntry{oldSnap: o + 1, newSnap: n + 1}
		switch op.kind {
		case 'c':
			o++
			n++
			e.line = DiffLine{Type: "ctx", Old: o, New: n, Text: oldLines[op.old]}
		case 'd':
			o++
			e.isChange = true
			e.line = DiffLine{Type: "del", Old: o, Text: oldLines[op.old]}
		case 'a':
			n++
			e.isChange = true
			e.line = DiffLine{Type: "add", New: n, Text: newLines[op.new]}
		}
		entries = append(entries, e)
	}

	var groups []DiffGroup
	for start := 0; start < len(entries); {
		if !entries[start].isChange {
			start++
			continue
		}
		end := start
		ctxSinceChange := 0
		for j := start; j < len(entries); j++ {
			if entries[j].isChange {
				end = j
				ctxSinceChange = 0
			} else {
				ctxSinceChange++
				if ctxSinceChange > diffMergeGap {
					break
				}
			}
		}
		lo := max(0, start-diffContext)
		hi := min(len(entries)-1, end+diffContext)
		g := DiffGroup{Lines: make([]DiffLine, 0, hi-lo+1)}
		oldCount, newCount := 0, 0
		for k := lo; k <= hi; k++ {
			g.Lines = append(g.Lines, entries[k].line)
			switch entries[k].line.Type {
			case "del":
				oldCount++
			case "add":
				newCount++
			default:
				oldCount++
				newCount++
			}
		}
		g.Header = fmt.Sprintf("@@ -%d,%d +%d,%d @@",
			entries[lo].oldSnap, oldCount, entries[lo].newSnap, newCount)
		groups = append(groups, g)
		start = hi + 1
	}
	return groups
}
