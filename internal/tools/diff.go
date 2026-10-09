package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
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
	maxDiffLines = 20_000
	maxDiffBytes = 1 << 20
)

// diffMaxCells bounds the LCS dynamic-programming table; larger middles fall
// back to a single coarse replace group instead of exploding memory. A var
// only so tests can shrink it.
var diffMaxCells = 4 << 20

// encoding/json's representation of invalid UTF-8 bytes varies between Go
// toolchains (literal U+FFFD versus an escaped replacement character). Match
// the actual runtime encoder instead of undercounting bounded diff results.
var invalidUTF8JSONSize = func() int {
	encoded, _ := json.Marshal(string([]byte{0xff})) // String marshaling cannot fail.
	return len(encoded) - 2                          // Exclude the JSON quotes.
}()

// DiffLines computes standard bounded change groups between two file
// contents. Identical inputs yield no groups.
func DiffLines(oldText, newText string) []DiffGroup {
	if oldText == newText {
		return nil
	}
	groups, _ := DiffLinesBounded(oldText, newText, maxDiffLines, maxDiffBytes)
	return groups
}

// DiffLinesBounded computes change groups while limiting input line work and
// the JSON-encoded groups returned. The bool reports that some or all output
// was omitted to stay within either budget.
func DiffLinesBounded(oldText, newText string, maxLines, maxBytes int) ([]DiffGroup, bool) {
	if maxLines < 0 || maxBytes < 2 {
		return nil, true
	}
	if oldText == newText {
		return []DiffGroup{}, false
	}
	oldCount := diffInputLineCount(oldText)
	newCount := diffInputLineCount(newText)
	if oldCount > maxLines || newCount > maxLines-oldCount {
		return []DiffGroup{}, true
	}

	oldLines := splitLines(oldText)
	newLines := splitLines(newText)
	if equalLines(oldLines, newLines) {
		return []DiffGroup{}, false
	}
	ops := diffOps(oldLines, newLines)
	if len(ops) == 0 {
		return []DiffGroup{}, false
	}
	groups := buildGroups(oldLines, newLines, ops)
	if len(groups) == 0 {
		return []DiffGroup{}, false
	}

	bounded := make([]DiffGroup, 0, len(groups))
	usedLines := 0
	usedBytes := 2 // JSON array brackets.
	for _, group := range groups {
		lineCount := 1 + len(group.Lines) // Header plus serialized diff lines.
		if lineCount > maxLines-usedLines {
			return bounded, true
		}
		separatorBytes := 0
		if len(bounded) > 0 {
			separatorBytes = 1
		}
		groupBytes, ok := diffGroupJSONSize(group, maxBytes-usedBytes-separatorBytes)
		if !ok {
			return bounded, true
		}
		usedBytes += separatorBytes + groupBytes
		if usedBytes > maxBytes {
			return bounded, true
		}
		usedLines += lineCount
		bounded = append(bounded, group)
	}
	return bounded, false
}

func diffInputLineCount(text string) int {
	if text == "" {
		return 0
	}
	count := strings.Count(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		count++
	}
	return count
}

func diffOutputLineCount(groups []DiffGroup) int {
	count := 0
	for _, group := range groups {
		count += 1 + len(group.Lines)
	}
	return count
}

func diffSliceJSONSize(groups []DiffGroup, maxBytes int) (int, bool) {
	if groups == nil {
		return addJSONSize(0, len("null"), maxBytes)
	}
	size := 2 // JSON array brackets.
	if size > maxBytes {
		return 0, false
	}
	for i, group := range groups {
		separatorBytes := 0
		if i > 0 {
			separatorBytes = 1
		}
		var ok bool
		size, ok = addJSONSize(size, separatorBytes, maxBytes)
		if !ok {
			return 0, false
		}
		groupBytes, ok := diffGroupJSONSize(group, maxBytes-size)
		if !ok {
			return 0, false
		}
		size, ok = addJSONSize(size, groupBytes, maxBytes)
		if !ok {
			return 0, false
		}
	}
	return size, true
}

func diffGroupJSONSize(group DiffGroup, maxBytes int) (int, bool) {
	size, ok := addJSONSize(0, len(`{"header":`), maxBytes)
	if !ok {
		return 0, false
	}
	stringBytes, ok := jsonStringSize(group.Header, maxBytes-size)
	if !ok {
		return 0, false
	}
	size, ok = addJSONSize(size, stringBytes, maxBytes)
	if !ok {
		return 0, false
	}
	size, ok = addJSONSize(size, len(`,"lines":[`), maxBytes)
	if !ok {
		return 0, false
	}
	for i, line := range group.Lines {
		if i > 0 {
			size, ok = addJSONSize(size, 1, maxBytes)
			if !ok {
				return 0, false
			}
		}
		lineBytes, ok := diffLineJSONSize(line, maxBytes-size)
		if !ok {
			return 0, false
		}
		size, ok = addJSONSize(size, lineBytes, maxBytes)
		if !ok {
			return 0, false
		}
	}
	size, ok = addJSONSize(size, len(`]}`), maxBytes)
	return size, ok
}

func diffLineJSONSize(line DiffLine, maxBytes int) (int, bool) {
	size, ok := addJSONSize(0, len(`{"type":`), maxBytes)
	if !ok {
		return 0, false
	}
	typeBytes, ok := jsonStringSize(line.Type, maxBytes-size)
	if !ok {
		return 0, false
	}
	size, ok = addJSONSize(size, typeBytes, maxBytes)
	if !ok {
		return 0, false
	}
	if line.Old != 0 {
		size, ok = addJSONSize(size, len(`,"old":`)+jsonIntSize(line.Old), maxBytes)
		if !ok {
			return 0, false
		}
	}
	if line.New != 0 {
		size, ok = addJSONSize(size, len(`,"new":`)+jsonIntSize(line.New), maxBytes)
		if !ok {
			return 0, false
		}
	}
	size, ok = addJSONSize(size, len(`,"text":`), maxBytes)
	if !ok {
		return 0, false
	}
	textBytes, ok := jsonStringSize(line.Text, maxBytes-size)
	if !ok {
		return 0, false
	}
	size, ok = addJSONSize(size, textBytes, maxBytes)
	if !ok {
		return 0, false
	}
	return addJSONSize(size, 1, maxBytes) // Closing object brace.
}

func jsonStringSize(text string, maxBytes int) (int, bool) {
	size := 2 // JSON quotes.
	if size > maxBytes {
		return 0, false
	}
	for i := 0; i < len(text); {
		b := text[i]
		if b < utf8.RuneSelf {
			delta := 1
			switch b {
			case '"', '\\', '\b', '\f', '\n', '\r', '\t':
				delta = 2
			case '<', '>', '&':
				delta = 6 // encoding/json HTML escapes these as \\u00XX.
			default:
				if b < 0x20 {
					delta = 6
				}
			}
			var ok bool
			size, ok = addJSONSize(size, delta, maxBytes)
			if !ok {
				return 0, false
			}
			i++
			continue
		}
		r, runeBytes := utf8.DecodeRuneInString(text[i:])
		delta := runeBytes
		if r == utf8.RuneError && runeBytes == 1 {
			delta = invalidUTF8JSONSize // Match the active encoding/json implementation.
		} else if r == '\u2028' || r == '\u2029' {
			delta = 6
		}
		var ok bool
		size, ok = addJSONSize(size, delta, maxBytes)
		if !ok {
			return 0, false
		}
		i += runeBytes
	}
	return size, true
}

func jsonIntSize(value int) int {
	size := 0
	if value <= 0 {
		size++
		value = -value
	}
	for value > 0 {
		size++
		value /= 10
	}
	return size
}

func addJSONSize(size, delta, maxBytes int) (int, bool) {
	if size < 0 || delta < 0 || size > maxBytes || delta > maxBytes-size {
		return 0, false
	}
	return size + delta, true
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
