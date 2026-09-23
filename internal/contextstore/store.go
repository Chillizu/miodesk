// Package contextstore persists lightweight cross-session working context.
//
// A context is not an AI session transcript. It is a small rolling handoff:
// where the work lives, what the current goal/state is, important decisions,
// open questions, and the next useful steps. The active handoff is mutable;
// checkpoints are immutable snapshots created at meaningful milestones.
package contextstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Chillizu/miodesk/internal/workspace"
)

const (
	maxRevisions   = 20
	maxCheckpoints = 50
)

var idPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// State is the semantic part of a rolling handoff. Fields are intentionally
// compact and future-facing: enough to resume work without replaying chat.
type State struct {
	Summary         string   `json:"summary,omitempty"`
	Goal            string   `json:"goal,omitempty"`
	CurrentState    string   `json:"current_state,omitempty"`
	ActiveReasoning string   `json:"active_reasoning,omitempty"`
	Decisions       []string `json:"decisions,omitempty"`
	OpenQuestions   []string `json:"open_questions,omitempty"`
	NextSteps       []string `json:"next_steps,omitempty"`
}

// Revision is an automatically retained copy of a previous active handoff.
type Revision struct {
	Number           int       `json:"number"`
	CreatedAt        time.Time `json:"created_at"`
	WorkingDirectory string    `json:"working_directory,omitempty"`
	State            State     `json:"state"`
}

// Checkpoint is an immutable named snapshot of the active handoff.
type Checkpoint struct {
	ID               string    `json:"id"`
	Label            string    `json:"label,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	Revision         int       `json:"revision"`
	WorkingDirectory string    `json:"working_directory,omitempty"`
	State            State     `json:"state"`
}

// Context is one long-lived line of work, addressable across AI hosts/sessions.
type Context struct {
	ID               string       `json:"id"`
	WorkingDirectory string       `json:"working_directory,omitempty"`
	State            State        `json:"state"`
	Revision         int          `json:"revision"`
	CheckpointSeq    int          `json:"checkpoint_seq,omitempty"`
	CreatedAt        time.Time    `json:"created_at"`
	UpdatedAt        time.Time    `json:"updated_at"`
	Revisions        []Revision   `json:"revisions,omitempty"`
	Checkpoints      []Checkpoint `json:"checkpoints,omitempty"`
}

// Active is the model-facing current handoff. Internal revision/checkpoint
// history is deliberately omitted so resume stays focused on what matters now.
type Active struct {
	ID               string    `json:"id"`
	WorkingDirectory string    `json:"working_directory,omitempty"`
	State            State     `json:"state"`
	Revision         int       `json:"revision"`
	CheckpointCount  int       `json:"checkpoint_count"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// Summary is the compact form returned by list.
type Summary struct {
	ID               string    `json:"id"`
	WorkingDirectory string    `json:"working_directory,omitempty"`
	Goal             string    `json:"goal,omitempty"`
	CurrentState     string    `json:"current_state,omitempty"`
	Revision         int       `json:"revision"`
	CheckpointCount  int       `json:"checkpoint_count"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// Input is the single model-facing context tool request.
//
// update upserts the rolling handoff. Only non-empty scalar fields and
// non-nil list fields replace their existing values.
// checkpoint may also include updates, which are applied before snapshotting.
// resume reads one active handoff. list returns compact context summaries.
type Input struct {
	Action           string   `json:"action" jsonschema:"context action: update, checkpoint, resume, or list"`
	ID               string   `json:"id,omitempty" jsonschema:"stable human-readable context id, e.g. miodesk-maintenance; required except for list"`
	WorkingDirectory string   `json:"working_directory,omitempty" jsonschema:"directory inside the miodesk workspace where this line of work lives"`
	Summary          string   `json:"summary,omitempty" jsonschema:"brief current handoff summary; update/checkpoint only"`
	Goal             string   `json:"goal,omitempty" jsonschema:"current goal; update/checkpoint only"`
	CurrentState     string   `json:"current_state,omitempty" jsonschema:"what is done/in progress now; update/checkpoint only"`
	ActiveReasoning  string   `json:"active_reasoning,omitempty" jsonschema:"current approach and why; update/checkpoint only"`
	Decisions        []string `json:"decisions,omitempty" jsonschema:"important decisions that should carry into the next session"`
	OpenQuestions    []string `json:"open_questions,omitempty" jsonschema:"unresolved questions or blockers"`
	NextSteps        []string `json:"next_steps,omitempty" jsonschema:"ordered next useful steps"`
	Label            string   `json:"label,omitempty" jsonschema:"optional checkpoint label; checkpoint only"`
}

// Output is the structured result of the context tool.
type Output struct {
	Kind       string      `json:"kind"`
	Action     string      `json:"action"`
	Context    *Active     `json:"context,omitempty"`
	Contexts   []Summary   `json:"contexts,omitempty"`
	Checkpoint *Checkpoint `json:"checkpoint,omitempty"`
}

// Store persists contexts as one JSON file per id.
type Store struct {
	dir string
	now func() time.Time
	mu  sync.Mutex
}

// New creates a store rooted at dir. Directories are created lazily.
func New(dir string) *Store {
	return &Store{dir: dir, now: time.Now}
}

// Apply executes one context action against ws.
func (s *Store) Apply(ws *workspace.Workspace, in Input) (*Output, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	action := strings.ToLower(strings.TrimSpace(in.Action))
	switch action {
	case "update":
		ctx, err := s.updateLocked(ws, in)
		if err != nil {
			return nil, err
		}
		return &Output{Kind: "context", Action: action, Context: activeView(ctx)}, nil
	case "checkpoint":
		ctx, cp, err := s.checkpointLocked(ws, in)
		if err != nil {
			return nil, err
		}
		return &Output{Kind: "context", Action: action, Context: activeView(ctx), Checkpoint: cp}, nil
	case "resume":
		if err := validateID(in.ID); err != nil {
			return nil, err
		}
		ctx, err := s.loadLocked(in.ID)
		if err != nil {
			return nil, err
		}
		return &Output{Kind: "context", Action: action, Context: activeView(ctx)}, nil
	case "list":
		items, err := s.listLocked()
		if err != nil {
			return nil, err
		}
		return &Output{Kind: "context", Action: action, Contexts: items}, nil
	default:
		return nil, fmt.Errorf("context: action must be update, checkpoint, resume, or list")
	}
}

func (s *Store) updateLocked(ws *workspace.Workspace, in Input) (*Context, error) {
	if err := validateID(in.ID); err != nil {
		return nil, err
	}

	ctx, err := s.loadLocked(in.ID)
	switch {
	case err == nil:
	case errors.Is(err, os.ErrNotExist):
		now := s.now().UTC()
		ctx = &Context{ID: in.ID, CreatedAt: now, UpdatedAt: now}
	default:
		return nil, err
	}

	changed, err := applyPatch(ws, ctx, in)
	if err != nil {
		return nil, err
	}
	if !changed {
		if ctx.Revision == 0 {
			return nil, fmt.Errorf("context update %q has no handoff content", in.ID)
		}
		return ctx, nil
	}

	if ctx.Revision > 0 {
		ctx.Revisions = append(ctx.Revisions, Revision{
			Number:           ctx.Revision,
			CreatedAt:        ctx.UpdatedAt,
			WorkingDirectory: ctx.WorkingDirectory,
			State:            cloneState(ctx.State),
		})
		if len(ctx.Revisions) > maxRevisions {
			ctx.Revisions = append([]Revision(nil), ctx.Revisions[len(ctx.Revisions)-maxRevisions:]...)
		}
	}

	ctx.Revision++
	ctx.UpdatedAt = s.now().UTC()
	if err := s.saveLocked(ctx); err != nil {
		return nil, err
	}
	return cloneContext(ctx), nil
}

func (s *Store) checkpointLocked(ws *workspace.Workspace, in Input) (*Context, *Checkpoint, error) {
	if err := validateID(in.ID); err != nil {
		return nil, nil, err
	}

	ctx, err := s.loadLocked(in.ID)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, nil, err
		}
		// A checkpoint with content may initialize a context in one call.
		ctx, err = s.updateLocked(ws, Input{
			Action:           "update",
			ID:               in.ID,
			WorkingDirectory: in.WorkingDirectory,
			Summary:          in.Summary,
			Goal:             in.Goal,
			CurrentState:     in.CurrentState,
			ActiveReasoning:  in.ActiveReasoning,
			Decisions:        in.Decisions,
			OpenQuestions:    in.OpenQuestions,
			NextSteps:        in.NextSteps,
		})
		if err != nil {
			return nil, nil, err
		}
	} else {
		changed, patchErr := applyPatch(ws, ctx, in)
		if patchErr != nil {
			return nil, nil, patchErr
		}
		if changed {
			// Reuse updateLocked so revision retention stays identical.
			ctx, err = s.updateLocked(ws, in)
			if err != nil {
				return nil, nil, err
			}
		}
	}

	if ctx.Revision == 0 {
		return nil, nil, fmt.Errorf("context checkpoint %q has no active handoff", in.ID)
	}

	ctx.CheckpointSeq++
	cp := Checkpoint{
		ID:               fmt.Sprintf("checkpoint-%03d", ctx.CheckpointSeq),
		Label:            strings.TrimSpace(in.Label),
		CreatedAt:        s.now().UTC(),
		Revision:         ctx.Revision,
		WorkingDirectory: ctx.WorkingDirectory,
		State:            cloneState(ctx.State),
	}
	ctx.Checkpoints = append(ctx.Checkpoints, cp)
	if len(ctx.Checkpoints) > maxCheckpoints {
		ctx.Checkpoints = append([]Checkpoint(nil), ctx.Checkpoints[len(ctx.Checkpoints)-maxCheckpoints:]...)
	}
	ctx.UpdatedAt = cp.CreatedAt
	if err := s.saveLocked(ctx); err != nil {
		return nil, nil, err
	}
	out := cloneContext(ctx)
	cpOut := cp
	return out, &cpOut, nil
}

func applyPatch(ws *workspace.Workspace, ctx *Context, in Input) (bool, error) {
	changed := false

	if in.WorkingDirectory != "" {
		resolved, err := ws.Resolve(in.WorkingDirectory)
		if err != nil {
			return false, fmt.Errorf("context working_directory: %w", err)
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return false, fmt.Errorf("context working_directory %q: %w", in.WorkingDirectory, err)
		}
		if !info.IsDir() {
			return false, fmt.Errorf("context working_directory %q is not a directory", in.WorkingDirectory)
		}

		// Resolve canonically for security, but preserve the caller-facing path
		// as the handoff locator. This keeps a familiar bind/symlink path such
		// as /home/user/Projects/foo instead of rewriting it to its backing path.
		displayPath := in.WorkingDirectory
		if !filepath.IsAbs(displayPath) {
			displayPath = filepath.Join(ws.Root(), displayPath)
		}
		displayPath = filepath.Clean(displayPath)
		if ctx.WorkingDirectory != displayPath {
			ctx.WorkingDirectory = displayPath
			changed = true
		}
	}

	replaceString := func(dst *string, src string) {
		src = strings.TrimSpace(src)
		if src != "" && *dst != src {
			*dst = src
			changed = true
		}
	}
	replaceString(&ctx.State.Summary, in.Summary)
	replaceString(&ctx.State.Goal, in.Goal)
	replaceString(&ctx.State.CurrentState, in.CurrentState)
	replaceString(&ctx.State.ActiveReasoning, in.ActiveReasoning)

	replaceList := func(dst *[]string, src []string) {
		if src == nil {
			return
		}
		next := cleanList(src)
		if !equalStrings(*dst, next) {
			*dst = next
			changed = true
		}
	}
	replaceList(&ctx.State.Decisions, in.Decisions)
	replaceList(&ctx.State.OpenQuestions, in.OpenQuestions)
	replaceList(&ctx.State.NextSteps, in.NextSteps)

	return changed, nil
}

func cleanList(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}

func equalStrings(a, b []string) bool {
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

func validateID(id string) error {
	id = strings.TrimSpace(id)
	if !idPattern.MatchString(id) {
		return fmt.Errorf("context id %q must match %s", id, idPattern.String())
	}
	return nil
}

func (s *Store) path(id string) string {
	return filepath.Join(s.dir, id+".json")
}

func (s *Store) loadLocked(id string) (*Context, error) {
	data, err := os.ReadFile(s.path(id))
	if err != nil {
		return nil, err
	}
	var ctx Context
	if err := json.Unmarshal(data, &ctx); err != nil {
		return nil, fmt.Errorf("read context %q: %w", id, err)
	}
	if ctx.ID != id {
		return nil, fmt.Errorf("read context %q: stored id is %q", id, ctx.ID)
	}
	return &ctx, nil
}

func (s *Store) listLocked() ([]Summary, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return []Summary{}, nil
	}
	if err != nil {
		return nil, err
	}

	items := make([]Summary, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".json")
		if !idPattern.MatchString(id) {
			continue
		}
		ctx, err := s.loadLocked(id)
		if err != nil {
			return nil, err
		}
		items = append(items, Summary{
			ID:               ctx.ID,
			WorkingDirectory: ctx.WorkingDirectory,
			Goal:             ctx.State.Goal,
			CurrentState:     ctx.State.CurrentState,
			Revision:         ctx.Revision,
			CheckpointCount:  len(ctx.Checkpoints),
			UpdatedAt:        ctx.UpdatedAt,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].ID < items[j].ID
		}
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	return items, nil
}

func (s *Store) saveLocked(ctx *Context) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("create context store: %w", err)
	}
	if err := os.Chmod(s.dir, 0o700); err != nil {
		return fmt.Errorf("secure context store: %w", err)
	}
	data, err := json.MarshalIndent(ctx, "", "  ")
	if err != nil {
		return fmt.Errorf("encode context %q: %w", ctx.ID, err)
	}
	data = append(data, '\n')

	f, err := os.CreateTemp(s.dir, "."+ctx.ID+"-*.tmp")
	if err != nil {
		return fmt.Errorf("write context %q: %w", ctx.ID, err)
	}
	tmp := f.Name()
	defer os.Remove(tmp)

	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, s.path(ctx.ID)); err != nil {
		return fmt.Errorf("commit context %q: %w", ctx.ID, err)
	}
	return nil
}

func activeView(in *Context) *Active {
	if in == nil {
		return nil
	}
	return &Active{
		ID:               in.ID,
		WorkingDirectory: in.WorkingDirectory,
		State:            cloneState(in.State),
		Revision:         in.Revision,
		CheckpointCount:  len(in.Checkpoints),
		CreatedAt:        in.CreatedAt,
		UpdatedAt:        in.UpdatedAt,
	}
}

func cloneState(in State) State {
	out := in
	out.Decisions = append([]string(nil), in.Decisions...)
	out.OpenQuestions = append([]string(nil), in.OpenQuestions...)
	out.NextSteps = append([]string(nil), in.NextSteps...)
	return out
}

func cloneContext(in *Context) *Context {
	if in == nil {
		return nil
	}
	out := *in
	out.State = cloneState(in.State)
	out.Revisions = append([]Revision(nil), in.Revisions...)
	for i := range out.Revisions {
		out.Revisions[i].State = cloneState(out.Revisions[i].State)
	}
	out.Checkpoints = append([]Checkpoint(nil), in.Checkpoints...)
	for i := range out.Checkpoints {
		out.Checkpoints[i].State = cloneState(out.Checkpoints[i].State)
	}
	return &out
}
