package daemon

import (
	"errors"
	"testing"

	"github.com/kyleking/wavez/internal/agent"
)

// Archiving is the one thing on a thread's cache that has to survive a
// daemon restart, so the reopen is what this test is about.
func TestManagerArchive(t *testing.T) {
	t.Parallel()

	logDir := t.TempDir()
	m := newManager(logDir, &agent.Loop{}, agent.Prefix{})

	mt, err := m.create(createParams{Prompt: "retire me", Dirs: []string{t.TempDir()}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	mt.running = true
	if err := m.setArchived(mt.id, true); !errors.Is(err, ErrThreadBusy) {
		t.Fatalf("archiving a working thread = %v, want %v", err, ErrThreadBusy)
	}
	mt.running = false

	if err := m.setArchived(mt.id, true); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if info, err := mt.info(); err != nil || !info.Archived {
		t.Fatalf("info archived = %v (err %v), want true", info.Archived, err)
	}

	reopened := newManager(logDir, &agent.Loop{}, agent.Prefix{})
	if err := reopened.reopen(); err != nil {
		t.Fatalf("reopen: %v", err)
	}

	back, ok := reopened.get(mt.id)
	if !ok {
		t.Fatalf("reopen lost thread %s", mt.id)
	}
	info, err := back.info()
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	if !info.Archived {
		t.Error("a reopened thread lost its archived position")
	}

	if err := reopened.setArchived(mt.id, false); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if info, err := back.info(); err != nil || info.Archived {
		t.Fatalf("info archived = %v (err %v), want false", info.Archived, err)
	}
}

// The working list and the archive are two lists, so a thread appears in
// exactly one of them.
func TestThreadsForProject_AnswersOneSideOfTheArchive(t *testing.T) {
	t.Parallel()

	m := newManager(t.TempDir(), &agent.Loop{}, agent.Prefix{})
	p := &Project{mgr: m, root: t.TempDir()}

	working, err := m.create(createParams{Prompt: "still going", Dirs: []string{p.root}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	put, err := m.create(createParams{Prompt: "put away", Dirs: []string{p.root}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := m.setArchived(put.id, true); err != nil {
		t.Fatalf("archive: %v", err)
	}

	for _, tc := range []struct {
		want     string
		archived bool
	}{
		{want: working.id},
		{want: put.id, archived: true},
	} {
		infos, err := threadsForProject(p, tc.archived)
		if err != nil {
			t.Fatalf("threadsForProject: %v", err)
		}
		if len(infos) != 1 || infos[0].ID != tc.want {
			t.Fatalf("archived=%v listed %d threads, want only %s", tc.archived, len(infos), tc.want)
		}
		if infos[0].Root != p.root {
			t.Errorf("Root = %q, want %q", infos[0].Root, p.root)
		}
	}
}
