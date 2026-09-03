package tools_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/tools"
)

type countingAsker struct {
	answer string
	asked  []string
}

func (c *countingAsker) Ask(_ context.Context, question string) (string, error) {
	c.asked = append(c.asked, question)

	return c.answer, nil
}

func demoCall(t *testing.T, d *tools.Demo, milestone, shows, reading string) string {
	t.Helper()

	result, err := d.Run(context.Background(), mustJSON(t, map[string]any{
		"milestone": milestone, "shows": shows, "reading": reading,
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.IsError {
		t.Fatalf("IsError = true, want false: %q", result.Content)
	}

	return result.Content
}

func TestDemoAsksOncePerMilestoneAndKeepsWhatWasSaid(t *testing.T) {
	t.Parallel()

	stateDir := t.TempDir()
	asker := &countingAsker{answer: "nobody scrubs a cassette by hand; make it the default view"}
	d := tools.NewDemo(stateDir, asker)

	first := demoCall(t, d, "Cassette scrubber",
		"`vcr-tui open fixtures/login.yaml` opens the scrubber",
		"you reach for it when a recorded run diverges and you want the request that changed")

	if !strings.Contains(first, asker.answer) {
		t.Errorf("first call did not carry the critique: %q", first)
	}
	if len(asker.asked) != 1 {
		t.Fatalf("asked %d times, want 1: %v", len(asker.asked), asker.asked)
	}
	if q := asker.asked[0]; !strings.Contains(q, "Cassette scrubber") ||
		!strings.Contains(q, "recorded run diverges") {
		t.Errorf("the user was not shown the milestone and the reading: %q", q)
	}

	// The same milestone spelled differently is the same milestone, so the
	// run is handed what it was told rather than asking again.
	second := demoCall(t, d, "cassette  SCRUBBER!", "unchanged", "unchanged")

	if len(asker.asked) != 1 {
		t.Errorf("asked %d times for one milestone, want 1: %v", len(asker.asked), asker.asked)
	}
	if !strings.Contains(second, asker.answer) {
		t.Errorf("second call did not repeat the critique: %q", second)
	}

	record := filepath.Join(stateDir, "demos", "cassette-scrubber.md")
	body, err := os.ReadFile(record) //nolint:gosec // a path this test built under its own TempDir
	if err != nil {
		t.Fatalf("reading the record: %v", err)
	}
	for _, want := range []string{"# Cassette scrubber", "login.yaml", "recorded run diverges", asker.answer} {
		if !strings.Contains(string(body), want) {
			t.Errorf("record is missing %q:\n%s", want, body)
		}
	}
}

func TestDemoRefusesACallThatShowsNothing(t *testing.T) {
	t.Parallel()

	full := map[string]any{"milestone": "m", "shows": "s", "reading": "r"}

	for _, drop := range []string{"milestone", "shows", "reading"} {
		t.Run(drop, func(t *testing.T) {
			t.Parallel()

			args := map[string]any{}
			for k, v := range full {
				args[k] = v
			}
			args[drop] = "   "

			asker := &countingAsker{answer: "unused"}
			result, err := tools.NewDemo(t.TempDir(), asker).
				Run(context.Background(), mustJSON(t, args))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !result.IsError {
				t.Errorf("IsError = false, want true with %s empty", drop)
			}
			if len(asker.asked) != 0 {
				t.Errorf("the user was asked despite %s being empty: %v", drop, asker.asked)
			}
		})
	}
}
