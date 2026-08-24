package app

import (
	"context"
	"slices"
	"testing"

	"github.com/kyleking/wavez/internal/tools"
)

type nopAsker struct{}

func (nopAsker) Ask(context.Context, string) (string, error) { return "", nil }

// A tool nothing can answer is worse than an absent one. Every one of the 8
// `question` calls in the recorded corpus failed with `reading answer: EOF`,
// because a replay's stdin is not a terminal, and each one cost a turn plus
// the 107 preamble tokens the tool carries on every other turn too.
func TestQuestionIsOfferedOnlyWhereSomethingCanAnswer(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		asker tools.Asker
		name  string
		want  bool
	}{
		{name: "an asker is wired", asker: nopAsker{}, want: true},
		{name: "nobody can answer", asker: nil, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			names := buildRegistry(registryDeps{root: t.TempDir(), asker: tc.asker}).Names()
			if got := slices.Contains(names, "question"); got != tc.want {
				t.Errorf("question offered = %v, want %v (tools: %v)", got, tc.want, names)
			}

			if !slices.Contains(names, "read") {
				t.Errorf("tools = %v, want the rest of the surface intact", names)
			}
		})
	}
}
