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
			name:       "an unpinned turn runs balanced",
			in:         router.Input{EstimatedTokens: 100},
			wantChoice: router.ChoiceBalanced,
			wantReason: "default tier",
		},
		{
			name:       "a request past the balanced tier's size still runs balanced",
			in:         router.Input{EstimatedTokens: router.FastBudget(0) * 100},
			wantChoice: router.ChoiceBalanced,
			wantReason: "default tier",
		},
		{
			name:       "a pin is honored",
			in:         router.Input{Override: router.ChoiceFast, EstimatedTokens: 100},
			wantChoice: router.ChoiceFast,
			wantReason: "explicit override",
		},
		{
			name:       "a pin to deep is honored",
			in:         router.Input{Override: router.ChoiceDeep, EstimatedTokens: 100},
			wantChoice: router.ChoiceDeep,
			wantReason: "explicit override",
		},
		{
			name:       "a fast pin over the served window moves up one tier",
			in:         router.Input{Override: router.ChoiceFast, EstimatedTokens: router.FastBudget(0) + 1},
			wantChoice: router.ChoiceBalanced,
			wantReason: "over the fast tier's context budget",
		},
		{
			name:       "a fast pin at the served window stays fast",
			in:         router.Input{Override: router.ChoiceFast, EstimatedTokens: router.FastBudget(0)},
			wantChoice: router.ChoiceFast,
			wantReason: "explicit override",
		},
		{
			name: "a wider served window admits a larger request on fast",
			in: router.Input{
				Override: router.ChoiceFast, EstimatedTokens: router.FastBudget(0) + 1, Window: 32768,
			},
			wantChoice: router.ChoiceFast,
			wantReason: "explicit override",
		},
		{
			name:       "one failure escalates one tier, not to the top",
			in:         router.Input{Override: router.ChoiceFast, PriorFailures: 1},
			wantChoice: router.ChoiceBalanced,
			wantReason: "escalated past fast after a failure",
		},
		{
			name:       "a second failure reaches the top tier",
			in:         router.Input{Override: router.ChoiceFast, PriorFailures: 2},
			wantChoice: router.ChoiceDeep,
			wantReason: "escalated past fast after a failure",
		},
		{
			name:       "an unpinned failure escalates from the default tier",
			in:         router.Input{PriorFailures: 1},
			wantChoice: router.ChoiceDeep,
			wantReason: "escalated past balanced after a failure",
		},
		{
			name:       "the top tier is never escalated past, however many failures",
			in:         router.Input{Override: router.ChoiceDeep, PriorFailures: 9},
			wantChoice: router.ChoiceDeep,
			wantReason: "no tier above deep",
		},
		{
			name: "the window check runs before escalation, so both apply",
			in: router.Input{
				Override: router.ChoiceFast, EstimatedTokens: router.FastBudget(0) + 1, PriorFailures: 1,
			},
			wantChoice: router.ChoiceDeep,
			wantReason: "escalated past balanced after a failure",
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

func TestChoiceValid(t *testing.T) {
	t.Parallel()

	for _, c := range []router.Choice{router.ChoiceFast, router.ChoiceBalanced, router.ChoiceDeep} {
		if !c.Valid() {
			t.Errorf("Choice(%q).Valid() = false, want true", c)
		}
	}
	for _, c := range []router.Choice{"", "local", "hosted", "Fast"} {
		if c.Valid() {
			t.Errorf("Choice(%q).Valid() = true, want false", c)
		}
	}
}

func TestTiersFor(t *testing.T) {
	t.Parallel()

	tiers := router.Tiers[string]{Fast: "fast-provider", Balanced: "balanced-provider", Deep: "deep-provider"}

	tests := []struct {
		name   string
		choice router.Choice
		want   string
	}{
		{name: "fast", choice: router.ChoiceFast, want: "fast-provider"},
		{name: "balanced", choice: router.ChoiceBalanced, want: "balanced-provider"},
		{name: "deep", choice: router.ChoiceDeep, want: "deep-provider"},
		{name: "a decision naming no tier falls back to the default tier", choice: "", want: "balanced-provider"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tiers.For(router.Decision{Choice: tt.choice}); got != tt.want {
				t.Errorf("For() = %q, want %q", got, tt.want)
			}
		})
	}
}
