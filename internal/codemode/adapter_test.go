package codemode

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Chillizu/miodesk/internal/workspace"
)

func fixture(t *testing.T) *workspace.Workspace {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"src/one.go": "package one\n// NEEDLE todo\n",
		"src/two.go": "package two\n// nothing here\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ws, err := workspace.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func TestCompositionSearchReadAndFilter(t *testing.T) {
	ws := fixture(t)
	script := `async () => {
		const results = await tools.search({query: "NEEDLE", path:"src"});
		const lines = [];
		for (const match of results.matches) {
			const item = await tools.read({path:match.file, offset:match.line,limit:1});
			lines.push({file:match.file, content:item.content.trim()});
		}
		return lines.filter(x => x.content.includes("todo"));
	}`
	out, err := Run(context.Background(), ws, script)
	if err != nil {
		t.Fatal(err)
	}
	if out.Calls != 2 {
		t.Fatalf("nested calls = %d, want 2", out.Calls)
	}
	var list []struct{ File, Content string }
	if err := json.Unmarshal(out.Value, &list); err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].File != filepath.Join("src", "one.go") || !strings.Contains(list[0].Content, "NEEDLE") {
		t.Fatalf("unexpected filtered result: %s", out.Value)
	}
}

func TestListReadReusesPaths(t *testing.T) {
	ws := fixture(t)
	out, err := Run(context.Background(), ws, `async () => {
		const files = (await tools.list({path:"src",depth:2})).entries;
		const first = files.find(x => x.name === "one.go");
		return (await tools.read({path:first.path,limit:1})).content;
	}`)
	if err != nil || string(out.Value) != `"package one\n"` {
		t.Fatalf("list/read = %+v, %v", out, err)
	}
}

func TestDenyWriteExecAndDirectHostAPIs(t *testing.T) {
	ws := fixture(t)
	for _, script := range []string{
		`async () => tools.write({path:"src/new",content:"bad"})`,
		`async () => tools.exec_command({cmd:"touch never"})`,
		`async () => require("fs").readFileSync("src/one.go")`,
		`async () => fetch("https://example.com")`,
	} {
		if _, err := Run(context.Background(), ws, script); err == nil {
			t.Errorf("unexposed capability succeeded: %q", script)
		}
	}
	if _, err := os.Stat(filepath.Join(ws.Root(), "src", "new")); !os.IsNotExist(err) {
		t.Fatalf("script created a file: %v", err)
	}
}

func TestWorkspaceEscapeCannotBeComposed(t *testing.T) {
	ws := fixture(t)
	_, err := Run(context.Background(), ws, `async () => await tools.read({path:"../../etc/passwd"})`)
	if err == nil {
		t.Fatal("workspace escape was accepted")
	}
}

func TestBoundedToolCallsAndOutput(t *testing.T) {
	ws := fixture(t)
	_, err := Run(context.Background(), ws, `async () => {
		for(let i=0;i<17;i++) await tools.read({path:"src/one.go"});
		return true;
	}`)
	if err == nil || !strings.Contains(err.Error(), "maximum 16 tool calls") {
		t.Fatalf("call budget error = %v", err)
	}
	_, err = Run(context.Background(), ws, `async () => "x".repeat(40000)`)
	if err == nil || !strings.Contains(err.Error(), "result exceeds") {
		t.Fatalf("response budget error = %v", err)
	}
}

func TestInterruptInfiniteLoopAndCancel(t *testing.T) {
	ws := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := Run(ctx, ws, `async () => { while(true) {} }`)
	if err == nil {
		t.Fatal("infinite loop finished")
	}
	if time.Since(start) > time.Second {
		t.Fatalf("interrupt took too long: %v", time.Since(start))
	}
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("ctx = %v", ctx.Err())
	}
}

func TestSourceLengthBound(t *testing.T) {
	ws := fixture(t)
	_, err := Run(context.Background(), ws, strings.Repeat("x", MaxSourceBytes+1))
	if err == nil || !strings.Contains(err.Error(), "source") {
		t.Fatalf("unbounded source admitted: %v", err)
	}
}
