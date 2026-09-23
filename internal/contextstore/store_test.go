package contextstore

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Chillizu/miodesk/internal/workspace"
)

func testStore(t *testing.T) (*Store, *workspace.Workspace, string) {
	t.Helper()
	root := t.TempDir()
	ws, err := workspace.New(root)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "contexts")
	s := New(dir)
	now := time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)
	s.now = func() time.Time {
		now = now.Add(time.Second)
		return now
	}
	return s, ws, root
}

func TestUpdateResumeAndList(t *testing.T) {
	s, ws, root := testStore(t)
	project := filepath.Join(root, "project")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := s.Apply(ws, Input{
		Action:           "update",
		ID:               "miodesk-maintenance",
		WorkingDirectory: project,
		Goal:             "Keep cross-chat work continuous",
		CurrentState:     "Designing rolling handoff",
		Decisions:        []string{"workspace root is not working_directory", "workspace root is not working_directory"},
		NextSteps:        []string{"wire the MCP tool"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Context.Revision != 1 || out.Context.WorkingDirectory != project {
		t.Fatalf("context = %+v", out.Context)
	}
	if len(out.Context.State.Decisions) != 1 {
		t.Fatalf("decisions = %#v", out.Context.State.Decisions)
	}

	out, err = s.Apply(ws, Input{
		Action:          "update",
		ID:              "miodesk-maintenance",
		CurrentState:    "Rolling handoff implemented",
		ActiveReasoning: "Update on meaningful state changes, not every message",
		OpenQuestions:   []string{},
		NextSteps:       []string{"add tests", "run validation"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Context.Revision != 2 {
		t.Fatalf("revision = %+v", out.Context)
	}
	stored, err := s.loadLocked("miodesk-maintenance")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Revisions) != 1 {
		t.Fatalf("revision history = %+v", stored.Revisions)
	}
	if out.Context.State.Goal != "Keep cross-chat work continuous" {
		t.Fatalf("goal should survive partial update: %+v", out.Context.State)
	}
	if out.Context.State.CurrentState != "Rolling handoff implemented" {
		t.Fatalf("state = %+v", out.Context.State)
	}

	resumed, err := s.Apply(ws, Input{Action: "resume", ID: "miodesk-maintenance"})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Context.Revision != 2 || resumed.Context.State.ActiveReasoning == "" {
		t.Fatalf("resume = %+v", resumed.Context)
	}

	listed, err := s.Apply(ws, Input{Action: "list"})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Contexts) != 1 || listed.Contexts[0].ID != "miodesk-maintenance" {
		t.Fatalf("list = %+v", listed.Contexts)
	}
}

func TestCheckpointSnapshotsActiveState(t *testing.T) {
	s, ws, root := testStore(t)
	project := filepath.Join(root, "project")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := s.Apply(ws, Input{
		Action:           "checkpoint",
		ID:               "feature-line",
		WorkingDirectory: project,
		Goal:             "Ship handoff",
		CurrentState:     "Schema agreed",
		Label:            "Design locked",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Checkpoint == nil || out.Checkpoint.ID != "checkpoint-001" || out.Checkpoint.Label != "Design locked" {
		t.Fatalf("checkpoint = %+v", out.Checkpoint)
	}
	if out.Context.CheckpointCount != 1 || out.Context.Revision != 1 {
		t.Fatalf("context = %+v", out.Context)
	}

	_, err = s.Apply(ws, Input{
		Action:       "update",
		ID:           "feature-line",
		CurrentState: "Implementation complete",
	})
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := s.Apply(ws, Input{Action: "resume", ID: "feature-line"})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Context.State.CurrentState != "Implementation complete" {
		t.Fatalf("active state = %+v", resumed.Context.State)
	}
	loaded, err := s.loadLocked("feature-line")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Checkpoints[0].State.CurrentState != "Schema agreed" {
		t.Fatalf("checkpoint mutated with active state: %+v", loaded.Checkpoints[0])
	}
}

func TestWorkingDirectoryMustStayInsideWorkspace(t *testing.T) {
	s, ws, _ := testStore(t)
	_, err := s.Apply(ws, Input{
		Action:           "update",
		ID:               "escape-test",
		WorkingDirectory: t.TempDir(),
		Goal:             "should fail",
	})
	if err == nil {
		t.Fatal("working_directory outside workspace should fail")
	}
}

func TestInvalidIDAndEmptyUpdate(t *testing.T) {
	s, ws, _ := testStore(t)
	if _, err := s.Apply(ws, Input{Action: "update", ID: "../oops", Goal: "x"}); err == nil {
		t.Fatal("invalid id should fail")
	}
	if _, err := s.Apply(ws, Input{Action: "update", ID: "empty"}); err == nil {
		t.Fatal("empty initial update should fail")
	}
}
