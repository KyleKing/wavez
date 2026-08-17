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

	if len(events) != 1 {
		t.Fatalf("child has %d event(s), want exactly the seed: %+v", len(events), events)
	}

	seed := events[0]
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

// A parent that changed nothing seeds nothing, so a fork of a fresh thread
// is not a thread with an empty change row in it.
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

	if len(events) != 0 {
		t.Errorf("child has %d event(s), want none: %+v", len(events), events)
	}
}
