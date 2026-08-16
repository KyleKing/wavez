package tui_test

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/tui"
)

// updateGolden reads the -update flag github.com/charmbracelet/x/exp/golden
// (pulled in transitively by teatest) already registers, rather than
// redefining a flag of the same name and panicking at test-binary init.
func updateGolden() bool {
	f := flag.Lookup("update")

	return f != nil && f.Value.String() == "true"
}

// fixedNow is the clock every test renders against, so ages are stable.
func fixedNow() time.Time { return time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC) }

// apply folds a sequence of messages through m.Update and returns the
// resulting concrete Model, so a test can keep chaining without repeating
// the tea.Model type assertion at every step.
func apply(t *testing.T, m tea.Model, msgs ...tea.Msg) tui.Model {
	t.Helper()

	for _, msg := range msgs {
		m, _ = m.Update(msg)
	}

	tm, ok := m.(tui.Model)
	require.True(t, ok)

	return tm
}

// newSized builds a Model and delivers its first WindowSizeMsg, which is
// what flips it into the ready state every other screen requires.
func newSized(t *testing.T, opts tui.Options, width, height int) tui.Model {
	t.Helper()

	if opts.Now == nil {
		opts.Now = fixedNow
	}

	return apply(t, tui.New(opts), tea.WindowSizeMsg{Width: width, Height: height})
}

func goldenCompare(t *testing.T, name, got string) {
	t.Helper()

	path := filepath.Join("testdata", name+".golden")

	if updateGolden() {
		require.NoError(t, os.WriteFile(path, []byte(got), 0o600))

		return
	}

	want, err := os.ReadFile(path) //nolint:gosec // path is testdata/<fixed name>.golden, never user input
	require.NoError(t, err)
	require.Equal(t, string(want), got)
}
