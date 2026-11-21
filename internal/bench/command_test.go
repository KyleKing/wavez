package bench_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/bench"
)

func TestCommand_FillsTheTemplate(t *testing.T) {
	t.Parallel()

	echo := script(t, "echoargs", `for a in "$@"; do echo "[$a]"; done`+"\n")
	dir := t.TempDir()

	tests := []struct {
		name     string
		template string
		want     []string
	}{
		{"prompt is one argument", echo + " -p {prompt}", []string{"[-p]", "[say 'hi' to  both]"}},
		{"model and dir", echo + " --model={model} --cwd {dir}", []string{"[--model=m1]", "[--cwd]", "[" + dir + "]"}},
		{
			"quoted words", echo + ` "two words" 'and three more' plain`,
			[]string{"[two words]", "[and three more]", "[plain]"},
		},
		{"empty quoted argument", echo + ` "" end`, []string{"[]", "[end]"}},
		{"extra whitespace", "  " + echo + "\t a  \n b ", []string{"[a]", "[b]"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			harness, err := bench.NewCommand(tt.template)
			require.NoError(t, err)

			got, err := harness.Run(t.Context(), bench.Request{Dir: dir, Prompt: "say 'hi' to  both", Model: "m1"})
			require.NoError(t, err)

			assert.Equal(t, tt.want, strings.Split(got.Answer, "\n"))
			assert.Equal(t, bench.StopCompleted, got.Stop)
			assert.Positive(t, got.Duration)
			assert.Zero(t, got.Turns)
			assert.Empty(t, got.SessionID)
		})
	}
}

func TestCommand_Failures(t *testing.T) {
	t.Parallel()

	_, err := bench.NewCommand("   ")
	require.ErrorIs(t, err, bench.ErrEmptyTemplate)

	partial, err := bench.NewCommand(script(t, "partial", "echo half an answer\nexit 2\n"))
	require.NoError(t, err)

	got, err := partial.Run(t.Context(), bench.Request{Dir: t.TempDir()})
	require.NoError(t, err)
	assert.Equal(t, bench.StopError, got.Stop)
	assert.Equal(t, "half an answer", got.Answer)

	silent, err := bench.NewCommand(script(t, "silent", "echo broke >&2\nexit 2\n"))
	require.NoError(t, err)

	_, err = silent.Run(t.Context(), bench.Request{Dir: t.TempDir()})
	require.ErrorContains(t, err, "broke")
}
