package daemon

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/tool"
)

// fakeRestorer stands in for the jj backend: changed reports what each
// successive ChangedFiles call sees, so a test can model a restore that
// cleaned the tree and one that did not.
type fakeRestorer struct {
	err          error
	stat         string
	changed      [][]string
	restoredPath []string
	calls        int
	restored     int
	scopedCalls  int
}

func (f *fakeRestorer) ChangedFiles(context.Context, string, string) ([]string, error) {
	if f.calls >= len(f.changed) {
		return nil, nil
	}

	out := f.changed[f.calls]
	f.calls++

	return out, nil
}

func (f *fakeRestorer) DiffStat(context.Context, string, string) (string, error) {
	return f.stat, nil
}

func (f *fakeRestorer) Restore(context.Context, string, string) error {
	f.restored++

	return f.err
}

func (f *fakeRestorer) RestorePaths(_ context.Context, _, _ string, paths []string) error {
	f.scopedCalls++
	f.restoredPath = paths

	return f.err
}

// fakeWriters scripts what the lease manager reports about other lanes.
type fakeWriters struct{ others []string }

func (w fakeWriters) OtherActiveHolders(_, _ string) []string { return w.others }

func TestManagerRestore(t *testing.T) {
	t.Parallel()

	const dirty = "a.go | 2 +-\n1 files changed, 1 insertions(+), 1 deletions(-)\n"

	tests := []struct {
		restorer     *fakeRestorer
		name         string
		wantErr      error
		baseline     string
		confirm      bool
		running      bool
		wantRestored bool
	}{
		{
			name:     "preview reports what it would discard without touching the tree",
			baseline: "op1",
			restorer: &fakeRestorer{stat: dirty, changed: [][]string{{"a.go"}}},
		},
		{
			name:         "confirm restores and verifies the tree came back clean",
			baseline:     "op1",
			confirm:      true,
			restorer:     &fakeRestorer{stat: dirty, changed: [][]string{{"a.go"}, nil}},
			wantRestored: true,
		},
		{
			name:     "an unchanged tree is refused rather than reported as undone",
			baseline: "op1",
			confirm:  true,
			restorer: &fakeRestorer{stat: "0 files changed", changed: [][]string{nil}},
			wantErr:  ErrNothingToRestore,
		},
		{
			name:     "a restore that leaves changes behind is a failure",
			baseline: "op1",
			confirm:  true,
			restorer: &fakeRestorer{stat: dirty, changed: [][]string{{"a.go"}, {"a.go"}}},
			wantErr:  ErrRestoreIncomplete,
		},
		{
			name:     "a thread that never ran has no checkpoint",
			restorer: &fakeRestorer{},
			wantErr:  ErrNoCheckpoint,
		},
		{
			name:     "a running thread is refused",
			baseline: "op1",
			running:  true,
			restorer: &fakeRestorer{stat: dirty, changed: [][]string{{"a.go"}}},
			wantErr:  ErrThreadBusy,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m := newManager(t.TempDir(), &agent.Loop{}, agent.Prefix{})

			mt, err := m.create(createParams{Prompt: "undo me", Dirs: []string{t.TempDir()}})
			if err != nil {
				t.Fatalf("create: %v", err)
			}

			mt.baseline = tc.baseline
			mt.running = tc.running

			cmd := api.Command{ThreadID: mt.id, Confirm: tc.confirm}

			got, err := m.restore(context.Background(), tc.restorer, nil, cmd)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("restore error = %v, want %v", err, tc.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatalf("restore: %v", err)
			}

			want := api.Restore{
				ThreadID: mt.id, Checkpoint: tc.baseline,
				Summary: tc.restorer.stat, Restored: tc.wantRestored,
			}
			checkRestore(t, got, want, tc.restorer, tc.confirm)
		})
	}
}

// checkRestore asserts the reported result and that only a confirmed undo
// reached the backend.
func checkRestore(t *testing.T, got, want api.Restore, r *fakeRestorer, confirm bool) {
	t.Helper()

	if !reflect.DeepEqual(got, want) {
		t.Errorf("restore = %+v, want %+v", got, want)
	}

	wantCalls := 0
	if confirm {
		wantCalls = 1
	}
	if r.restored != wantCalls {
		t.Errorf("Restore called %d time(s), want %d", r.restored, wantCalls)
	}
}

func TestManagerRestoreWithoutARestorer(t *testing.T) {
	t.Parallel()

	m := newManager(t.TempDir(), &agent.Loop{}, agent.Prefix{})

	mt, err := m.create(createParams{Prompt: "undo me", Dirs: []string{t.TempDir()}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	mt.baseline = "op1"

	_, err = m.restore(context.Background(), nil, nil, api.Command{ThreadID: mt.id, Confirm: true})
	if !errors.Is(err, ErrNoRepository) {
		t.Fatalf("restore error = %v, want %v", err, ErrNoRepository)
	}
}

// The operation ids ride on each accepted change so undo reaches one edit
// rather than only the whole run. An id the thread never recorded is
// refused: restoring destroys uncommitted work, and a client naming an
// arbitrary operation is not a request to carry out.
func TestManagerRestoreTargetsOneRecordedEdit(t *testing.T) {
	t.Parallel()

	m := newManager(t.TempDir(), &agent.Loop{}, agent.Prefix{})

	mt, err := m.create(createParams{Prompt: "undo me", Dirs: []string{t.TempDir()}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	mt.baseline = "op0"
	mt.edits = []api.EditPoint{{Op: "op1", Tool: "str_replace", Paths: []string{"a.go"}}}

	r := &fakeRestorer{changed: [][]string{{"a.go"}, {"a.go"}}, stat: "a.go | 1 +\n"}

	got, err := m.restore(context.Background(), r, nil, api.Command{ThreadID: mt.id, Checkpoint: "op1"})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}

	if got.Checkpoint != "op1" {
		t.Errorf("Checkpoint = %q, want the picked edit point", got.Checkpoint)
	}

	if len(got.Edits) != 1 {
		t.Errorf("Edits = %v, want the picker's own list back", got.Edits)
	}

	_, err = m.restore(context.Background(), r, nil, api.Command{ThreadID: mt.id, Checkpoint: "opX"})
	if !errors.Is(err, ErrUnknownCheckpoint) {
		t.Errorf("restore error = %v, want %v", err, ErrUnknownCheckpoint)
	}
}

// Sending to a working thread used to return ErrThreadBusy and drop the
// text, so a user who thought of something mid-run had to wait for it to
// stop and retype. It queues now and starts at a turn boundary.
func TestManagerQueuesAPromptSentMidTurn(t *testing.T) {
	t.Parallel()

	m := newManager(t.TempDir(), &agent.Loop{}, agent.Prefix{})

	mt, err := m.create(createParams{Prompt: "first", Dirs: []string{t.TempDir()}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	mt.mu.Lock()
	mt.running = true
	mt.mu.Unlock()

	if err := m.send(mt.id, "and also this", false); err != nil {
		t.Fatalf("send while running = %v, want it queued", err)
	}

	if got := m.queued(mt.id); got != 1 {
		t.Fatalf("queued = %d, want the prompt held rather than dropped", got)
	}

	mt.mu.Lock()
	mt.running = false
	pending := mt.pending
	mt.mu.Unlock()

	if len(pending) != 1 || pending[0] != "and also this" {
		t.Errorf("pending = %v, want the prompt verbatim", pending)
	}
}

// A whole-repo operation restore takes every writer's work with it, so a
// thread undoing itself while another lane writes must revert only the paths
// it recorded an edit to. Which mechanism runs is the scheduler's call, and
// the client is told which one it got.
func checkMechanism(t *testing.T, r *fakeRestorer, wantWhole, wantScoped int) {
	t.Helper()

	if r.restored != wantWhole {
		t.Errorf("Restore called %d time(s), want %d", r.restored, wantWhole)
	}
	if r.scopedCalls != wantScoped {
		t.Errorf("RestorePaths called %d time(s), want %d", r.scopedCalls, wantScoped)
	}
	if wantScoped > 0 && !reflect.DeepEqual(r.restoredPath, []string{"mine.go"}) {
		t.Errorf("RestorePaths got %v, want only this thread's edited paths", r.restoredPath)
	}
}

func TestManagerRestoreScopesToOwnPathsWhileAnotherLaneWrites(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		others      []string
		leftover    []string
		wantWhole   int
		wantScopedN int
		wantScoped  bool
	}{
		{name: "alone in the tree, the whole operation comes back", wantWhole: 1},
		{
			name:        "another lane writing, only this thread's paths come back",
			others:      []string{"other-thread"},
			wantScoped:  true,
			wantScopedN: 1,
			leftover:    []string{"theirs.go"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m := newManager(t.TempDir(), &agent.Loop{}, agent.Prefix{})

			mt, err := m.create(createParams{Prompt: "undo me", Dirs: []string{t.TempDir()}})
			if err != nil {
				t.Fatalf("create: %v", err)
			}

			mt.baseline = "op0"
			mt.edits = []api.EditPoint{{Op: "op1", Tool: "str_replace", Paths: []string{"mine.go"}}}

			// The second ChangedFiles is the verification: the neighbor's file
			// still differs after a scoped restore, and that must not read as
			// a restore that failed.
			r := &fakeRestorer{
				stat:    "mine.go | 1 +\n",
				changed: [][]string{{"mine.go", "theirs.go"}, tc.leftover},
			}

			got, err := m.restore(context.Background(), r, fakeWriters{others: tc.others},
				api.Command{ThreadID: mt.id, Confirm: true})
			if err != nil {
				t.Fatalf("restore: %v", err)
			}

			if got.Scoped != tc.wantScoped {
				t.Errorf("Scoped = %v, want %v", got.Scoped, tc.wantScoped)
			}
			if !got.Restored {
				t.Error("Restored = false, want the undo reported as done")
			}

			checkMechanism(t, r, tc.wantWhole, tc.wantScopedN)
		})
	}
}

// A thread log written before repositories were tracked carries one bare
// checkpoint per tool call, and every existing thread's log is that shape.
// Dropping those events would take per-edit undo away from all of them.
func TestApplyEditPointReadsALegacyCheckpoint(t *testing.T) {
	t.Parallel()

	m := newManager(t.TempDir(), &agent.Loop{}, agent.Prefix{})

	mt, err := m.create(createParams{Prompt: "undo me", Dirs: []string{t.TempDir()}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	mt.applyEditPoint(event.Event{
		Kind:    event.KindTool,
		Tool:    "str_replace",
		Changes: []tool.Change{{Path: "a.go"}},
		Detail:  map[string]any{"checkpoint": "op1"},
	})

	want := []api.EditPoint{{Op: "op1", Tool: "str_replace", Paths: []string{"a.go"}}}
	if !reflect.DeepEqual(mt.edits, want) {
		t.Errorf("edits = %+v, want %+v", mt.edits, want)
	}
}

// A restore spanning repositories reports one of their checkpoints and
// concatenates their diff stats, so the order has to come from the thread
// rather than from a map's iteration.
func TestManagerRestoreOrdersRepositoriesBeforeReporting(t *testing.T) {
	t.Parallel()

	m := newManager(t.TempDir(), &agent.Loop{}, agent.Prefix{})

	own, sibling := t.TempDir(), t.TempDir()

	mt, err := m.create(createParams{Prompt: "undo me", Dirs: []string{own}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	mt.baseline = "op0"
	mt.edits = []api.EditPoint{
		{Repo: sibling, Op: "op-sibling", Tool: "write", Paths: []string{"hk.pkl"}},
		{Repo: own, Op: "op-own", Tool: "str_replace", Paths: []string{"a.go"}},
	}

	for range 8 {
		r := &fakeRestorer{stat: "a.go | 1 +\n", changed: [][]string{{"a.go"}, {"hk.pkl"}}}

		got, rErr := m.restore(context.Background(), r, nil, api.Command{ThreadID: mt.id, Confirm: true})
		if rErr != nil {
			t.Fatalf("restore: %v", rErr)
		}

		if got.Checkpoint != "op-own" {
			t.Fatalf("Checkpoint = %q, want the thread's own repository's", got.Checkpoint)
		}

		if r.restored != 2 {
			t.Fatalf("Restore called %d time(s), want both repositories reverted", r.restored)
		}
	}
}
