package contextstore

import (
	"bytes"
	"encoding/json"
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
		ApproachSummary: "Update on meaningful state changes, not every message",
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
	if resumed.Context.Revision != 2 || resumed.Context.State.ApproachSummary == "" {
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

func TestResumeMigratesLegacyActiveReasoning(t *testing.T) {
	s, ws, _ := testStore(t)
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	fixture := `{"id":"legacy-context","state":{"summary":"Existing","active_reasoning":"use the replay harness"},"revision":1}`
	if err := os.WriteFile(s.path("legacy-context"), []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}

	resumed, err := s.Apply(ws, Input{Action: "resume", ID: "legacy-context"})
	if err != nil {
		t.Fatalf("resume legacy context: %v", err)
	}
	if resumed.Context.State.ApproachSummary != "use the replay harness" {
		t.Fatalf("approach summary = %q", resumed.Context.State.ApproachSummary)
	}
	if _, err := s.Apply(ws, Input{Action: "update", ID: "legacy-context", CurrentState: "Migrated"}); err != nil {
		t.Fatalf("update legacy context: %v", err)
	}
	stored, err := os.ReadFile(s.path("legacy-context"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(stored, []byte(`"approach_summary"`)) || !bytes.Contains(stored, []byte("use the replay harness")) {
		t.Errorf("updated context lacks approach_summary: %s", stored)
	}
	if bytes.Contains(stored, []byte(`"active_reasoning"`)) {
		t.Errorf("updated context retained legacy active_reasoning key: %s", stored)
	}
}

func TestInputUnmarshalMigratesLegacyActiveReasoning(t *testing.T) {
	var legacy Input
	if err := json.Unmarshal([]byte(`{"action":"update","id":"legacy","active_reasoning":"use the replay harness"}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.ApproachSummary != "use the replay harness" {
		t.Fatalf("legacy approach summary = %q", legacy.ApproachSummary)
	}

	var both Input
	if err := json.Unmarshal([]byte(`{"action":"update","id":"both","active_reasoning":"old value","approach_summary":"new value"}`), &both); err != nil {
		t.Fatal(err)
	}
	if both.ApproachSummary != "new value" {
		t.Fatalf("new field did not take precedence: %q", both.ApproachSummary)
	}
}

func TestInputUnmarshalRejectsUnknownFields(t *testing.T) {
	var in Input
	if err := json.Unmarshal([]byte(`{"action":"update","id":"unknown","unexpected":"value"}`), &in); err == nil {
		t.Fatal("unknown context input field should be rejected")
	}
}

func TestUpdateClearsScalarFields(t *testing.T) {
	s, ws, root := testStore(t)
	project := filepath.Join(root, "project")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(ws, Input{
		Action:           "update",
		ID:               "clear-scalars",
		WorkingDirectory: project,
		Summary:          "summary",
		Goal:             "goal",
		CurrentState:     "current",
		ApproachSummary:  "approach",
	}); err != nil {
		t.Fatalf("initial update: %v", err)
	}

	cleared, err := s.Apply(ws, Input{
		Action: "update",
		ID:     "clear-scalars",
		ClearFields: []string{
			"summary", "goal", "current_state", "approach_summary", "working_directory",
		},
	})
	if err != nil {
		t.Fatalf("clear update: %v", err)
	}
	if cleared.Context.WorkingDirectory != "" || cleared.Context.State.Summary != "" ||
		cleared.Context.State.Goal != "" || cleared.Context.State.CurrentState != "" ||
		cleared.Context.State.ApproachSummary != "" {
		t.Errorf("scalar fields were not cleared: %+v", cleared.Context)
	}
	if ws.Root() != root {
		t.Errorf("clearing the locator changed workspace root to %q", ws.Root())
	}
}

func TestUpdateRejectsInvalidClearFields(t *testing.T) {
	s, ws, _ := testStore(t)
	if _, err := s.Apply(ws, Input{Action: "update", ID: "invalid-clear", Summary: "keep me"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(s.path("invalid-clear"))
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name  string
		input Input
	}{
		{name: "duplicate", input: Input{Action: "update", ID: "invalid-clear", ClearFields: []string{"summary", "summary"}}},
		{name: "unknown", input: Input{Action: "update", ID: "invalid-clear", ClearFields: []string{"unexpected"}}},
		{name: "resume", input: Input{Action: "resume", ID: "invalid-clear", ClearFields: []string{"summary"}}},
		{name: "list", input: Input{Action: "list", ClearFields: []string{"summary"}}},
		{name: "empty-on-resume", input: Input{Action: "resume", ID: "invalid-clear", ClearFields: []string{}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.Apply(ws, tc.input); err == nil {
				t.Fatal("invalid clear_fields request unexpectedly succeeded")
			}
			after, readErr := os.ReadFile(s.path("invalid-clear"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !bytes.Equal(after, before) {
				t.Fatalf("invalid request changed saved state:\nbefore %s\nafter  %s", before, after)
			}
		})
	}

	var nullFields Input
	if err := json.Unmarshal([]byte(`{"action":"list","clear_fields":null}`), &nullFields); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(ws, nullFields); err == nil {
		t.Fatal("explicit null clear_fields on list should be rejected")
	}
	after, err := os.ReadFile(s.path("invalid-clear"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatalf("null clear_fields changed saved state:\nbefore %s\nafter  %s", before, after)
	}
}

func TestClearFieldWinsAndIncrementsOnce(t *testing.T) {
	s, ws, _ := testStore(t)
	initial, err := s.Apply(ws, Input{Action: "update", ID: "clear-wins", Summary: "original"})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := s.Apply(ws, Input{
		Action:      "update",
		ID:          "clear-wins",
		Summary:     "temporary value",
		ClearFields: []string{"summary"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Context.State.Summary != "" || updated.Context.Revision != initial.Context.Revision+1 {
		t.Fatalf("clear precedence/revision = %+v", updated.Context)
	}
}

func TestCheckpointClearsScalarFields(t *testing.T) {
	s, ws, root := testStore(t)
	project := filepath.Join(root, "project")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(ws, Input{
		Action:           "update",
		ID:               "checkpoint-clear",
		WorkingDirectory: project,
		Summary:          "summary",
		ApproachSummary:  "approach",
	}); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := s.Apply(ws, Input{
		Action:      "checkpoint",
		ID:          "checkpoint-clear",
		Label:       "cleared",
		ClearFields: []string{"working_directory", "approach_summary"},
	})
	if err != nil {
		t.Fatalf("checkpoint clear: %v", err)
	}
	if checkpoint.Context.WorkingDirectory != "" || checkpoint.Context.State.ApproachSummary != "" {
		t.Errorf("active state was not cleared: %+v", checkpoint.Context)
	}
	if checkpoint.Checkpoint == nil || checkpoint.Checkpoint.WorkingDirectory != "" || checkpoint.Checkpoint.State.ApproachSummary != "" {
		t.Errorf("checkpoint did not snapshot the cleared state: %+v", checkpoint.Checkpoint)
	}
	if ws.Root() != root {
		t.Errorf("clearing the locator changed workspace root to %q", ws.Root())
	}
}

func TestCheckpointInitializationCarriesApproachSummaryAndClearFields(t *testing.T) {
	s, ws, root := testStore(t)
	project := filepath.Join(root, "project")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	created, err := s.Apply(ws, Input{
		Action:           "checkpoint",
		ID:               "checkpoint-init",
		WorkingDirectory: project,
		Summary:          "initial summary",
		ApproachSummary:  "keep this approach",
		ClearFields:      []string{"working_directory"},
		Label:            "first",
	})
	if err != nil {
		t.Fatalf("initialize checkpoint: %v", err)
	}
	if created.Context.WorkingDirectory != "" || created.Context.State.ApproachSummary != "keep this approach" {
		t.Errorf("initialized active state = %+v", created.Context)
	}
	if created.Checkpoint == nil || created.Checkpoint.WorkingDirectory != "" || created.Checkpoint.State.ApproachSummary != "keep this approach" {
		t.Errorf("initialized checkpoint = %+v", created.Checkpoint)
	}
	if ws.Root() != root {
		t.Errorf("checkpoint locator clear changed workspace root to %q", ws.Root())
	}
	stateJSON, err := json.Marshal(created.Context.State)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(stateJSON, []byte(`"approach_summary"`)) || bytes.Contains(stateJSON, []byte(`"active_reasoning"`)) {
		t.Fatalf("checkpoint state has wrong field names: %s", stateJSON)
	}
}
