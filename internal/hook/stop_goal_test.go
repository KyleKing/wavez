package hook_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/hook"
)

var errGoalFile = errors.New(".wavez/goal.toml: line 3:\n  unknown key")

// goalAnswer is what a fake goal check returns at one stop.
type goalAnswer struct {
	err    error
	person string
	agent  string
	hold   bool
}

func goalsFrom(answers *[]goalAnswer) hook.Option {
	return hook.WithGoals(func(context.Context, string) (string, string, bool, error) {
		next := (*answers)[0]
		if len(*answers) > 1 {
			*answers = (*answers)[1:]
		}

		return next.person, next.agent, next.hold, next.err
	})
}

func TestHandle_Stop_Goal(t *testing.T) {
	t.Parallel()

	unmet := goalAnswer{
		person: "goal 1/2: not done: documented", agent: "Goal not done: x\n- documented: no match", hold: true,
	}
	met := goalAnswer{person: "goal 2/2 done"}

	tests := []struct {
		name    string
		first   string
		answers []goalAnswer
		want    []string
		touch   bool
	}{
		{
			name:    "a goal that does not hold the turn is a line for the person, shown once",
			answers: []goalAnswer{{person: "goal 1/2: not done: documented"}},
			want:    []string{"told", "silent"},
			first:   "wavez: goal 1/2: not done: documented",
		},
		{
			name:    "the line follows the gates' own",
			answers: []goalAnswer{met},
			touch:   true,
			want:    []string{"told", "silent"},
			first:   "wavez: 1 file changed · checks passed · goal 2/2 done",
		},
		{
			name:    "an unmet goal that holds the turn blocks until it is met",
			answers: []goalAnswer{unmet, met},
			want:    []string{"block", "told"},
			first:   "wavez: goal 1/2: not done: documented",
		},
		{
			name:    "the same unmet goal is let go after the round limit",
			answers: []goalAnswer{unmet},
			want:    []string{"block", "released", "silent"},
			first:   "wavez: goal 1/2: not done: documented",
		},
		{
			name:    "a goal file that cannot be read never holds the turn",
			answers: []goalAnswer{{err: errGoalFile, hold: true, agent: "ignored"}},
			want:    []string{"told", "silent"},
			first:   "wavez: goal not checked: .wavez/goal.toml: line 3: unknown key",
		},
		{
			name:    "no goal and no change says nothing",
			answers: []goalAnswer{{}},
			want:    []string{"silent"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := repo(t)
			answers := tt.answers
			h := hook.New(&fakeRunner{verdicts: []hook.Verdict{green}}, hook.WithMaxRounds(2), goalsFrom(&answers))
			start(t, h, root)

			if tt.touch {
				write(t, root, "a.go", "package a // changed\n")
			}

			for i, want := range tt.want {
				res, err := fire(t, h, stop(root, i > 0))
				require.NoError(t, err, "stop %d", i)
				assert.Equal(t, want, outcome(res), "stop %d", i)

				if i == 0 {
					assert.Equal(t, tt.first, res.SystemMessage)
				}

				if want == "block" {
					assert.Equal(t, unmet.agent, res.Reason, "stop %d", i)
				}
			}
		})
	}
}

func TestHandle_Stop_GoalWindowFollowsTheStopBudget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		opts   []hook.Option
		window time.Duration
	}{
		{"the default budget", nil, 170 * time.Second},
		{"a configured budget", []hook.Option{hook.WithStopBudget(60 * time.Second)}, 80 * time.Second},
		{"a budget that is not positive is ignored", []hook.Option{hook.WithStopBudget(0)}, 170 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var left time.Duration

			goals := hook.WithGoals(func(ctx context.Context, _ string) (string, string, bool, error) {
				deadline, ok := ctx.Deadline()
				require.True(t, ok, "the goal check has a deadline")

				left = time.Until(deadline)

				return "", "", false, nil
			})

			root := repo(t)
			h := hook.New(&fakeRunner{verdicts: []hook.Verdict{green}}, append(tt.opts, goals)...)
			start(t, h, root)

			_, err := fire(t, h, stop(root, false))
			require.NoError(t, err)
			assert.InDelta(t, tt.window.Seconds(), left.Seconds(), 5)
		})
	}
}
