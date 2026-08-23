package daemon

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/api"
)

// fakeRestorer stands in for the jj backend: changed reports what each
// successive ChangedFiles call sees, so a test can model a restore that
// cleaned the tree and one that did not.
type fakeRestorer struct {
	err      error
	stat     string
	changed  [][]string
	calls    int
	restored int
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

			got, err := m.restore(context.Background(), tc.restorer, api.Command{ThreadID: mt.id, Confirm: tc.confirm})

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

	_, err = m.restore(context.Background(), nil, api.Command{ThreadID: mt.id, Confirm: true})
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

	got, err := m.restore(context.Background(), r, api.Command{ThreadID: mt.id, Checkpoint: "op1"})
	if err != nil {
		t.Fatalf("restore: %v", err)
	}

	if got.Checkpoint != "op1" {
		t.Errorf("Checkpoint = %q, want the picked edit point", got.Checkpoint)
	}

	if len(got.Edits) != 1 {
		t.Errorf("Edits = %v, want the picker's own list back", got.Edits)
	}

	_, err = m.restore(context.Background(), r, api.Command{ThreadID: mt.id, Checkpoint: "opX"})
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
