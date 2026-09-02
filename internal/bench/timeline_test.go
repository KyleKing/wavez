package bench_test

import (
	"strings"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/bench"
	"github.com/kyleking/wavez/internal/event"
)

// The timeline is the same log Summarize reads, as a sequence: what a
// reader is after is where the calls and the gate rounds fell, so the
// boundary between turns and what lands on which side of it is the whole
// property.
func TestTimeline_SplitsAtTheTurnMarkerAndCarriesWhatHappened(t *testing.T) {
	t.Parallel()

	base := time.Unix(0, 0)
	at := func(d time.Duration, ev event.Event) event.Event {
		ev.At = base.Add(d)

		return ev
	}

	turns := bench.Timeline([]event.Event{
		at(0, event.Event{Kind: event.KindUser, Text: "go"}),
		at(2*time.Second, read("a.go", "x")),
		at(3*time.Second, turn("balanced", 100, 10, 0)),
		at(4*time.Second, event.Event{
			Kind: event.KindTool, Tool: "str_replace", Text: "no match",
			Detail: map[string]any{"is_error": true, "cause": "no_match"},
		}),
		// Not delivered, so the run was never told: it belongs to no turn.
		at(5*time.Second, event.Event{Kind: event.KindGate, Detail: map[string]any{"round": 1, "pass": false}}),
		at(6*time.Second, event.Event{Kind: event.KindGate, Detail: map[string]any{"delivered": true, "pass": false}}),
		at(7*time.Second, event.Event{
			Kind: event.KindError, Detail: map[string]any{"escalated": true, "tier": "balanced"},
		}),
		at(13*time.Second, turn("deep", 200, 20, 0)),
	})

	if len(turns) != 2 {
		t.Fatalf("timeline has %d turns, want 2", len(turns))
	}

	if got := turns[0].Duration; got != 3*time.Second {
		t.Errorf("turn 1 lasted %s, want 3s", got)
	}
	if len(turns[0].Calls) != 1 || turns[0].Calls[0].Tool != "read" {
		t.Errorf("turn 1 calls = %+v, want one read", turns[0].Calls)
	}
	if len(turns[0].Notes) != 0 {
		t.Errorf("turn 1 notes = %v, want none", turns[0].Notes)
	}

	if got := turns[1].Duration; got != 10*time.Second {
		t.Errorf("turn 2 lasted %s, want 10s", got)
	}
	if !turns[1].Calls[0].Error || turns[1].Calls[0].Cause != "no_match" {
		t.Errorf("turn 2 call = %+v, want a no_match failure", turns[1].Calls[0])
	}

	want := []string{"gates failed", "balanced failed, moved up"}
	if strings.Join(turns[1].Notes, "|") != strings.Join(want, "|") {
		t.Errorf("turn 2 notes = %v, want %v", turns[1].Notes, want)
	}

	var out strings.Builder
	if err := bench.RenderTimeline(&out, turns); err != nil {
		t.Fatalf("RenderTimeline: %v", err)
	}

	rendered := out.String()
	for _, want := range []string{"str_replace ✗no_match", "· gates failed", "· now on deep"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered timeline = %q, want it to carry %q", rendered, want)
		}
	}

	// The longest turn fills the bar and the shorter one does not, which is
	// what makes the column readable at a glance.
	lines := strings.Split(strings.TrimRight(rendered, "\n"), "\n")
	if strings.Count(lines[0], "█") >= strings.Count(lines[1], "█") {
		t.Errorf("bars = %q and %q, want the longer turn to draw more", lines[0], lines[1])
	}
}

// A turn parked for an approval is not a turn that worked for five minutes,
// and reading one that way makes the slowest turn of a session the one where
// the human went to lunch.
func TestTimeline_TakesTheWaitForAHumanOutOfTheTurn(t *testing.T) {
	t.Parallel()

	base := time.Unix(0, 0)
	at := func(d time.Duration, ev event.Event) event.Event {
		ev.At = base.Add(d)

		return ev
	}

	turns := bench.Timeline([]event.Event{
		at(0, event.Event{Kind: event.KindUser, Text: "go"}),
		at(1*time.Second, event.Event{Kind: event.KindState, State: event.StateNeedsIn}),
		at(5*time.Minute, event.Event{Kind: event.KindState, State: event.StateWorking}),
		at(5*time.Minute+2*time.Second, turn("balanced", 100, 10, 0)),
	})

	if len(turns) != 1 {
		t.Fatalf("timeline has %d turns, want 1", len(turns))
	}

	// One second before it parked and two after, against five minutes of
	// wall clock.
	if got := turns[0].Duration; got != 3*time.Second {
		t.Errorf("turn lasted %s, want the 3s it worked rather than the wall clock", got)
	}

	if got := turns[0].Waited; got != 5*time.Minute-time.Second {
		t.Errorf("turn waited %s, want the parked time kept", got)
	}

	var out strings.Builder
	if err := bench.RenderTimeline(&out, turns); err != nil {
		t.Fatalf("RenderTimeline: %v", err)
	}

	if !strings.Contains(out.String(), "waited") {
		t.Errorf("rendered timeline = %q, want it to name the wait", out.String())
	}
}
