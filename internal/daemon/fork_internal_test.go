package daemon

import (
	"testing"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/tool"
)

// A fork carries the parent's change set and nothing else: the transcript
// is re-derivable from the tree, the list of files already touched is not.
func TestSeedFromParentCarriesChangesNotProse(t *testing.T) {
	t.Parallel()

	m := newManager(t.TempDir(), &agent.Loop{}, agent.Prefix{})

	parent, err := m.create(createParams{Prompt: "fix the lease ttl", Dirs: []string{t.TempDir()}})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}

	for _, ev := range []event.Event{
		{Kind: event.KindUser, Text: "make the lease TTL configurable"},
		{Kind: event.KindAgent, Text: "I will start by reading lease.go"},
		{Kind: event.KindTool, Tool: "str_replace", Changes: []tool.Change{
			{Path: "internal/lease/lease.go", Added: 6, Removed: 2, Ranges: []tool.LineRange{{Start: 10, End: 15}}},
		}},
		{Kind: event.KindTool, Tool: "write", Changes: []tool.Change{{Path: "internal/lease/ttl.go", Added: 3}}},
	} {
		if _, err := parent.th.Log().Append(ev); err != nil {
			t.Fatalf("appending to parent: %v", err)
		}
	}

	child, err := m.create(createParams{Prompt: "try it the other way", Parent: parent.id, Dirs: []string{t.TempDir()}})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	events, err := child.th.Log().Since(0)
	if err != nil {
		t.Fatalf("reading child: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("child has %d event(s), want the goal and the seed: %+v", len(events), events)
	}

	if events[0].Kind != event.KindGoal || events[0].Text != "fix the lease ttl" {
		t.Errorf("child's first event = %+v, want the parent's goal", events[0])
	}

	seed := events[1]
	if len(seed.Changes) != 2 {
		t.Fatalf("seed carries %d change(s), want 2: %+v", len(seed.Changes), seed.Changes)
	}

	for _, e := range events {
		if e.Text == "I will start by reading lease.go" {
			t.Error("the parent's prose crossed into the fork")
		}
	}

	if seed.Changes[0].Ranges[0].Start != 10 {
		t.Errorf("line ranges did not cross: %+v", seed.Changes[0])
	}
}

// A parent that changed nothing seeds no change row, so a fork of a fresh
// thread is not a thread with an empty change row in it. The goal crosses
// either way: it is what the fork is for, and a parent that has written
// nothing yet still has one.
func TestSeedFromParentSkipsAnUntouchedTree(t *testing.T) {
	t.Parallel()

	m := newManager(t.TempDir(), &agent.Loop{}, agent.Prefix{})

	parent, err := m.create(createParams{Prompt: "nothing yet", Dirs: []string{t.TempDir()}})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}

	child, err := m.create(createParams{Parent: parent.id, Dirs: []string{t.TempDir()}})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	events, err := child.th.Log().Since(0)
	if err != nil {
		t.Fatalf("reading child: %v", err)
	}

	if len(events) != 1 || events[0].Kind != event.KindGoal {
		t.Errorf("child has %d event(s), want only the inherited goal: %+v", len(events), events)
	}
}
