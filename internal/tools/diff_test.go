package tools

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestDiffLinesBoundedStopsBeforeSplittingDenseInput(t *testing.T) {
	groups, truncated := DiffLinesBounded(strings.Repeat("\n", 20001), "changed\n", 20000, 1<<20)
	if !truncated {
		t.Fatal("dense input should be truncated before splitting")
	}
	lines := 0
	for _, group := range groups {
		lines += 1 + len(group.Lines)
	}
	if lines > 20000 {
		t.Fatalf("serialized diff lines = %d, want at most 20000", lines)
	}
	encoded, err := json.Marshal(groups)
	if err != nil {
		t.Fatalf("marshal bounded diff: %v", err)
	}
	if len(encoded) > 1<<20 {
		t.Fatalf("serialized diff is %d bytes, want at most %d", len(encoded), 1<<20)
	}
}

func TestDiffLinesBoundedCountsGroupHeadersAgainstLineBudget(t *testing.T) {
	groups, truncated := DiffLinesBounded("a\nb\nc\n", "x\ny\nz\n", 6, 1<<20)
	if !truncated {
		t.Fatal("group header should consume the line budget")
	}
	if len(groups) != 0 {
		t.Fatalf("groups = %d, want no partial group", len(groups))
	}
}

func TestDiffLinesBoundedCapsJSONEscapes(t *testing.T) {
	text := strings.Repeat("<", 200_000) + "\"\\\b\f\n\r\t\x00\x1f"
	groups, truncated := DiffLinesBounded("", text, 20000, 1<<20)
	if !truncated {
		t.Fatal("escaped JSON output should be truncated")
	}
	encoded, err := json.Marshal(groups)
	if err != nil {
		t.Fatalf("marshal bounded diff: %v", err)
	}
	if len(encoded) > 1<<20 {
		t.Fatalf("serialized diff is %d bytes, want at most %d", len(encoded), 1<<20)
	}
}

func TestDiffJSONSizeMatchesEncodingJSON(t *testing.T) {
	texts := []string{
		"plain text",
		"<tag>&value",
		"quote \" slash \\",
		"controls\b\f\n\r\t\x00\x1f",
		"separators\u2028\u2029",
		"unicode 雪🙂",
		string([]byte{0xff, 'x', 0xc0, 0xaf}),
	}
	for _, text := range texts {
		encoded, err := json.Marshal(text)
		if err != nil {
			t.Fatalf("marshal %q: %v", text, err)
		}
		got, ok := jsonStringSize(text, len(encoded))
		if !ok || got != len(encoded) {
			t.Errorf("jsonStringSize(%q) = %d, %v; encoding/json size = %d", text, got, ok, len(encoded))
		}

		groups := []DiffGroup{{
			Header: text,
			Lines:  []DiffLine{{Type: "add", New: 123, Text: text}},
		}}
		encoded, err = json.Marshal(groups)
		if err != nil {
			t.Fatalf("marshal groups with %q: %v", text, err)
		}
		got, ok = diffSliceJSONSize(groups, len(encoded))
		if !ok || got != len(encoded) {
			t.Errorf("diffSliceJSONSize(%q) = %d, %v; encoding/json size = %d", text, got, ok, len(encoded))
		}
	}
}

func BenchmarkDiffLinesBoundedDense(b *testing.B) {
	oldText := strings.Repeat("old\n", 20001)
	newText := strings.Repeat("new\n", 20001)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, truncated := DiffLinesBounded(oldText, newText, 20000, 1<<20)
		if !truncated {
			b.Fatal("dense diff should be truncated")
		}
	}
}

func TestDiffIdentical(t *testing.T) {
	if got := DiffLines("a\nb\n", "a\nb\n"); got != nil {
		t.Errorf("identical inputs should produce no groups, got %+v", got)
	}
}

func TestDiffSimpleReplace(t *testing.T) {
	groups := DiffLines("alpha\nbeta\ngamma\n", "alpha\nBETA\ngamma\n")
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(groups))
	}
	g := groups[0]
	var kinds []string
	for _, l := range g.Lines {
		kinds = append(kinds, l.Type)
	}
	// 3 context lines around the change → padded with context on both sides.
	if !reflect.DeepEqual(kinds, []string{"ctx", "del", "add", "ctx"}) {
		t.Errorf("line kinds = %v", kinds)
	}
	if g.Lines[1].Text != "beta" || g.Lines[2].Text != "BETA" {
		t.Errorf("texts = %q %q", g.Lines[1].Text, g.Lines[2].Text)
	}
	if g.Lines[1].Old != 2 || g.Lines[2].New != 2 {
		t.Errorf("numbers wrong: %+v", g.Lines)
	}
	// The file is 3 lines, so context padding clamps to the whole file.
	if !strings.Contains(g.Header, "@@ -1,3 +1,3 @@") {
		t.Errorf("header = %q", g.Header)
	}
}

func TestDiffInsertionAndDeletion(t *testing.T) {
	groups := DiffLines("one\ntwo\nthree\n", "one\ntwo\nnew\nthree\n")
	if len(groups) != 1 {
		t.Fatalf("groups = %d", len(groups))
	}
	var hasAdd bool
	for _, l := range groups[0].Lines {
		if l.Type == "add" && l.Text == "new" {
			hasAdd = true
		}
	}
	if !hasAdd {
		t.Errorf("insertion not represented: %+v", groups[0].Lines)
	}

	groups = DiffLines("one\ntwo\nthree\n", "one\nthree\n")
	if len(groups) != 1 {
		t.Fatalf("groups = %d", len(groups))
	}
	var hasDel bool
	for _, l := range groups[0].Lines {
		if l.Type == "del" && l.Text == "two" {
			hasDel = true
		}
	}
	if !hasDel {
		t.Errorf("deletion not represented: %+v", groups[0].Lines)
	}
}

func TestDiffTwoDistantChangesMakeTwoGroups(t *testing.T) {
	var oldLines, newLines []string
	for i := 0; i < 40; i++ {
		oldLines = append(oldLines, "line")
		if i == 0 {
			newLines = append(newLines, "changed-start")
		} else {
			newLines = append(newLines, "line")
		}
	}
	newLines[39] = "changed-end"
	oldText := strings.Join(oldLines, "\n") + "\n"
	newText := strings.Join(newLines, "\n") + "\n"

	groups := DiffLines(oldText, newText)
	if len(groups) != 2 {
		t.Fatalf("groups = %d, want 2 (distant changes must not merge)", len(groups))
	}
	for _, g := range groups {
		if len(g.Lines) > 3+1+3 {
			t.Errorf("group too large: %d lines", len(g.Lines))
		}
	}
}

func TestDiffAdjoiningChangesMerge(t *testing.T) {
	// Two single-line changes separated by 3 context lines must merge into
	// one group (gap <= 2*diffContext).
	old := "a\nX\nb\nc\nd\ne\nY\nf\n"
	new := "a\nx\nb\nc\nd\ne\ny\nf\n"
	groups := DiffLines(old, new)
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1 (adjoining changes merge)", len(groups))
	}
}

func TestDiffLargeFileCoarseFallback(t *testing.T) {
	oldCells := diffMaxCells
	diffMaxCells = 16 // force the coarse path
	defer func() { diffMaxCells = oldCells }()

	oldText := "a\nb\nc\nd\n"
	newText := "a\nx\ny\nd\n"
	groups := DiffLines(oldText, newText)
	if len(groups) != 1 {
		t.Fatalf("groups = %d, want 1 coarse group", len(groups))
	}
	kinds := map[string]int{}
	for _, l := range groups[0].Lines {
		kinds[l.Type]++
	}
	if kinds["del"] != 2 || kinds["add"] != 2 {
		t.Errorf("coarse group = %+v", groups[0].Lines)
	}
}

func TestSplitLines(t *testing.T) {
	if got := splitLines(""); got != nil {
		t.Errorf("splitLines(\"\") = %v, want nil", got)
	}
	if got := splitLines("a"); !reflect.DeepEqual(got, []string{"a"}) {
		t.Errorf("splitLines(\"a\") = %v", got)
	}
	if got := splitLines("a\nb\n"); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("splitLines = %v", got)
	}
}
