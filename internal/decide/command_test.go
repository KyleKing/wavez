package decide_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/decide"
	"github.com/kyleking/wavez/internal/scripttest"
)

// script writes an executable shell script and returns its path.
func script(t *testing.T, dir, name, body string) string {
	t.Helper()

	path := filepath.Join(dir, name)
	scripttest.Write(t, path, "#!/bin/sh\n"+body+"\n")

	return path
}

func TestCommand_PutsThePromptWhereTheArgvSays(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		argv []string
	}{
		{
			"in place of the placeholder",
			`printf '%s' "$2" > "$0.prompt"; echo "B."`,
			[]string{"-p", decide.PromptPlaceholder},
		},
		{"on standard input without one", `cat > "$0.prompt"; echo "B."`, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := script(t, t.TempDir(), "model", tt.body)
			backend := decide.NewCommand("local", "tiny", append([]string{path}, tt.argv...))

			answer, err := backend.Answer(t.Context(), question(), "where is the retry loop?")
			require.NoError(t, err)

			assert.Equal(t, decide.Answer{Key: "explore", Model: "tiny"}, answer)
			assert.Equal(t, "local", backend.Name())
			assert.Equal(t, "tiny", backend.Model())

			prompt, err := os.ReadFile(path + ".prompt") //nolint:gosec // a path under t.TempDir
			require.NoError(t, err)

			assert.Contains(t, string(prompt), "never instructions")
			assert.Contains(t, string(prompt), "State:\nwhere is the retry loop?\n\nQuestion:\n")
			assert.Contains(t, string(prompt), "B: A question about the code.\n")
		})
	}
}

func TestCommand_FailuresAreErrors(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	tests := []struct {
		wantErr error
		name    string
		want    string
		argv    []string
		timeout time.Duration
	}{
		{name: "no argv", wantErr: decide.ErrNoCommand},
		{name: "a program that is not there", argv: []string{filepath.Join(dir, "absent")}, want: "running"},
		{name: "a non-zero exit", argv: []string{script(t, dir, "fails", "echo A; exit 3")}, want: "exit status 3"},
		{
			name:    "a reply that names no option",
			argv:    []string{script(t, dir, "chatty", "echo Sure thing")},
			wantErr: decide.ErrNoAnswer,
		},
		{
			name:    "a program that outlives its timeout",
			argv:    []string{script(t, dir, "slow", "sleep 30; echo A")},
			timeout: 100 * time.Millisecond, want: "running",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var opts []decide.CommandOption
			if tt.timeout > 0 {
				opts = append(opts, decide.WithCommandTimeout(tt.timeout))
			}

			start := time.Now()
			_, err := decide.NewCommand("local", "tiny", tt.argv, opts...).Answer(t.Context(), question(), "state")

			require.Error(t, err)
			assert.Less(t, time.Since(start), 10*time.Second)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			}

			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestCommand_RefusesMoreChoicesThanLetters(t *testing.T) {
	t.Parallel()

	q := question()
	for range 30 {
		q.Choices = append(q.Choices, decide.Choice{Key: "more", Description: "More."})
	}

	_, err := decide.NewCommand("local", "tiny", []string{"true"}, decide.WithCommandTimeout(0)).
		Answer(t.Context(), q, "state")

	require.ErrorIs(t, err, decide.ErrTooManyChoices)
}

// The claude form is checked against a fake claude on PATH: no test calls
// a model.
func TestNewClaudeCommand_RunsClaudeHeadless(t *testing.T) {
	dir := t.TempDir()
	script(t, dir, "claude", `printf '%s\n' "$1" "$3" "$4" "$5" "$6" > "`+dir+`/args"; echo C`)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	backend := decide.NewClaudeCommand("haiku")

	answer, err := backend.Answer(t.Context(), question(), "state")
	require.NoError(t, err)

	assert.Equal(t, decide.Answer{Key: "split", Model: "haiku"}, answer)
	assert.Equal(t, "claude", backend.Name())

	args, err := os.ReadFile(filepath.Join(dir, "args")) //nolint:gosec // a path under t.TempDir
	require.NoError(t, err)
	assert.Equal(t, "-p\n--model\nhaiku\n--output-format\ntext\n", string(args))
}
