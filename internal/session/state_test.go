package session_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kyleking/wavez/internal/session"
)

func TestState_Fail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		prints []string
		want   []int
	}{
		{name: "same failure counts up", prints: []string{"x", "x", "x"}, want: []int{1, 2, 3}},
		{name: "a new failure starts over", prints: []string{"x", "x", "y", "y"}, want: []int{1, 2, 1, 2}},
		{name: "a failure that returns is new again", prints: []string{"x", "y", "x"}, want: []int{1, 1, 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var state session.State

			got := make([]int, 0, len(tt.prints))
			for _, failure := range tt.prints {
				got = append(got, state.Fail(failure, "set-"+failure))
			}

			assert.Equal(t, tt.want, got)
			assert.Equal(t, "set-"+tt.prints[len(tt.prints)-1], state.Failure.ChangeSet)
		})
	}
}

func TestState_Pass(t *testing.T) {
	t.Parallel()

	var state session.State

	state.Fail("x", "one")
	state.Fail("x", "two")
	state.Pass("three")

	assert.Equal(t, "three", state.Passed)
	assert.Equal(t, session.Failure{}, state.Failure)
	assert.Equal(t, 1, state.Fail("x", "four"), "a pass in between breaks the run of failures")
}
