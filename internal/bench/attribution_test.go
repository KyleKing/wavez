package bench_test

import (
	"testing"

	"github.com/kyleking/wavez/internal/bench"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/tool"
)

// A run's turn count says nothing about where the run went. Two of the
// three classes are exact from the log, and the third is the one worth
// having: a turn spent reacting to a gate message or a failed call is one
// the task never asked for.
func TestAttributeSplitsTurnsByWhatTheyWentTo(t *testing.T) {
	t.Parallel()

	agent := event.Event{Kind: event.KindAgent, Role: event.RoleNote}
	evs := []event.Event{
		{Kind: event.KindUser, Text: "make truncate cut on a boundary"},
		agent,
		{Kind: event.KindTool, Tool: "read", Text: "a file"},
		agent,
		{Kind: event.KindTool, Tool: "str_replace", Changes: []tool.Change{{Path: "a.go"}}},
		{Kind: event.KindUser, Text: "Gates ran on your changes and found this: …"},
		agent,
		{Kind: event.KindTool, Tool: "str_replace", Detail: map[string]any{"is_error": true}},
		agent,
		{Kind: event.KindTool, Tool: "str_replace", Changes: []tool.Change{{Path: "a.go"}}},
		{Kind: event.KindAgent, Role: event.RoleAnswer},
	}

	got := bench.Attribute(evs)

	want := bench.Attribution{Productive: 1, Retrieval: 1, Harness: 2, Prose: 1}
	if got != want {
		t.Errorf("Attribute() = %+v, want %+v", got, want)
	}

	if got.Total() != 5 {
		t.Errorf("Total() = %d, want every turn attributed once", got.Total())
	}
}
