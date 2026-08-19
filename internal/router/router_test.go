package router_test

import (
	"testing"

	"github.com/kyleking/wavez/internal/router"
)

func TestRoute(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		wantChoice router.Choice
		wantReason string
		in         router.Input
	}{
		{
			name:       "single file within budget stays local",
			in:         router.Input{FileCount: 1, EstimatedTokens: 100},
			wantChoice: router.ChoiceLocal,
			wantReason: "single-file task within local context budget",
		},
		{
			name:       "zero file count stays local",
			in:         router.Input{FileCount: 0, EstimatedTokens: 0},
			wantChoice: router.ChoiceLocal,
			wantReason: "single-file task within local context budget",
		},
		{
			name:       "multi-file escalates",
			in:         router.Input{FileCount: 2, EstimatedTokens: 100},
			wantChoice: router.ChoiceHosted,
			wantReason: "multi-file task",
		},
		{
			name:       "over local context budget escalates",
			in:         router.Input{FileCount: 1, EstimatedTokens: router.LocalBudget(0) + 1},
			wantChoice: router.ChoiceHosted,
			wantReason: "over local context budget",
		},
		{
			name:       "at local context budget stays local",
			in:         router.Input{FileCount: 1, EstimatedTokens: router.LocalBudget(0)},
			wantChoice: router.ChoiceLocal,
			wantReason: "single-file task within local context budget",
		},
		{
			name:       "one prior failure escalates even for a small task",
			in:         router.Input{FileCount: 1, EstimatedTokens: 100, PriorFailures: 1},
			wantChoice: router.ChoiceHosted,
			wantReason: "prior local failure",
		},
		{
			name:       "multiple prior failures still escalate, never retried",
			in:         router.Input{FileCount: 1, EstimatedTokens: 100, PriorFailures: 3},
			wantChoice: router.ChoiceHosted,
			wantReason: "prior local failure",
		},
		{
			name:       "a wider served window admits a larger request",
			in:         router.Input{FileCount: 1, EstimatedTokens: router.LocalBudget(0) + 1, Window: 32768},
			wantChoice: router.ChoiceLocal,
			wantReason: "single-file task within local context budget",
		},
		{
			name: "explicit override beats every other rule",
			in: router.Input{
				FileCount: 5, EstimatedTokens: router.LocalBudget(0) + 1,
				PriorFailures: 2, Override: router.ChoiceLocal,
			},
			wantChoice: router.ChoiceLocal,
			wantReason: "explicit override",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := router.Route(tt.in)
			if got.Choice != tt.wantChoice {
				t.Errorf("Choice = %q, want %q", got.Choice, tt.wantChoice)
			}
			if got.Reason != tt.wantReason {
				t.Errorf("Reason = %q, want %q", got.Reason, tt.wantReason)
			}
		})
	}
}

func TestSelect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		choice router.Choice
		want   string
	}{
		{name: "local", choice: router.ChoiceLocal, want: "local-provider"},
		{name: "hosted", choice: router.ChoiceHosted, want: "hosted-provider"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := router.Select(router.Decision{Choice: tt.choice}, "local-provider", "hosted-provider")
			if got != tt.want {
				t.Errorf("Select() = %q, want %q", got, tt.want)
			}
		})
	}
}
