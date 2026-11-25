package statusline_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kyleking/wavez/internal/goal"
	"github.com/kyleking/wavez/internal/session"
	"github.com/kyleking/wavez/internal/statusline"
)

func percent(value float64) *float64 { return &value }

func TestRender(t *testing.T) {
	t.Parallel()

	failing := &session.State{Failure: session.Failure{Rounds: 3}}
	fixing := &session.State{Failure: session.Failure{Rounds: 1}}
	passed := &session.State{Passed: "abc"}
	abstained := &session.State{Passed: "abc", Abstained: "def"}

	tests := []struct {
		name string
		want string
		in   statusline.Input
	}{
		{name: "nothing known prints nothing"},
		{
			name: "prompts answered with the code and its tests are counted for a larger model",
			in: statusline.Input{
				State: &session.State{Passed: "abc", Prompts: 4, Briefed: 3}, Model: "claude-sonnet-5-5",
			},
			want: "wavez · checks passed · 3 of 4 prompts fit haiku",
		},
		{
			name: "a session on the smallest model is told nothing about it",
			in: statusline.Input{
				State: &session.State{Passed: "abc", Prompts: 4, Briefed: 3}, Model: "claude-haiku-5-5",
			},
			want: "wavez · checks passed",
		},
		{
			name: "no prompt answered in full says nothing",
			in:   statusline.Input{State: &session.State{Passed: "abc", Prompts: 2}, Model: "claude-sonnet-5-5"},
			want: "wavez · checks passed",
		},
		{
			name: "a quiet session shows progress only",
			in:   statusline.Input{State: passed, ContextPercent: percent(41.6), CostUSD: 0.314, MaxRounds: 3},
			want: "wavez · checks passed · context 42% · $0.31",
		},
		{
			name: "checks the gates gave up on lead the line",
			in:   statusline.Input{State: failing, ContextPercent: percent(20), MaxRounds: 3},
			want: "wavez · ! checks failing, agent let go · context 20%",
		},
		{
			name: "a failure still being fixed is progress, not a request",
			in:   statusline.Input{State: fixing, MaxRounds: 3},
			want: "wavez · fixing checks, round 1",
		},
		{
			name: "checks that could not run need the person",
			in:   statusline.Input{State: abstained, MaxRounds: 3},
			want: "wavez · ! checks could not run",
		},
		{
			name: "a full context window is asked about before progress is shown",
			in: statusline.Input{
				State: passed, ContextPercent: percent(86), MaxRounds: 3,
				Goal: &goal.Summary{Total: 3, Done: 1, Unmet: []string{"documented", "no TODO left"}},
			},
			want: "wavez · ! context 86% full · checks passed · goal 1/3, next: documented",
		},
		{
			name: "a goal condition that could not be checked is the person's",
			in:   statusline.Input{Goal: &goal.Summary{Total: 2, Done: 1, CouldNotCheck: 1}},
			want: "wavez · ! 1 goal condition could not be checked · goal 1/2",
		},
		{
			name: "a goal waiting for its end-of-turn check says so",
			in:   statusline.Input{Goal: &goal.Summary{Total: 2, Done: 1, Unknown: 1, Stale: true}},
			want: "wavez · goal 1/2, checked at the end of the turn",
		},
		{
			name: "a goal that is done",
			in:   statusline.Input{Goal: &goal.Summary{Total: 2, Done: 2}},
			want: "wavez · goal 2/2 done",
		},
		{
			name: "a narrow terminal loses the last parts first",
			in: statusline.Input{
				State: failing, ContextPercent: percent(20), CostUSD: 1, MaxRounds: 3, Width: 40,
				Goal: &goal.Summary{Total: 3, Done: 1, Unmet: []string{"documented"}},
			},
			want: "wavez · ! checks failing, agent let go",
		},
		{
			name: "color marks what needs the person",
			in:   statusline.Input{State: abstained, MaxRounds: 3, Color: true},
			want: "\x1b[2mwavez\x1b[0m · \x1b[31m! checks could not run\x1b[0m",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, statusline.Render(tt.in))
		})
	}
}
