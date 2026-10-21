package codemode

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Chillizu/miodesk/internal/tools"
	"github.com/Chillizu/miodesk/internal/workspace"
)

// These measure local dispatch overhead, not total model tokens or reasoning
// time. Both benchmarks perform the same list -> read composition.
func benchWorkspace(b *testing.B) *workspace.Workspace {
	b.Helper()
	root := b.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "project"), 0o755); err != nil {
		b.Fatal(err)
	}
	for _, filename := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(root, "project", filename), []byte("package project\n"), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	ws, err := workspace.New(root)
	if err != nil {
		b.Fatal(err)
	}
	return ws
}

func BenchmarkDirectToolComposition(b *testing.B) {
	ws := benchWorkspace(b)
	ctx := context.Background()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		items, err := tools.List(ctx, ws, tools.ListInput{Path: "project", Depth: 1})
		if err != nil {
			b.Fatal(err)
		}
		for _, item := range items.Entries {
			if item.Name == "a.go" {
				if _, err := tools.Read(ctx, ws, tools.ReadInput{Path: item.Path, Limit: 1}); err != nil {
					b.Fatal(err)
				}
			}
		}
	}
}

func BenchmarkCodeModeComposition(b *testing.B) {
	ws := benchWorkspace(b)
	ctx := context.Background()
	source := `async () => {
		const entries = (await tools.list({path: "project", depth: 1})).entries;
		const file = entries.find(e => e.name === "a.go");
		return (await tools.read({path: file.path, limit: 1})).content;
	}`
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Run(ctx, ws, source); err != nil {
			b.Fatal(err)
		}
	}
}
