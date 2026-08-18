package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/kyleking/wavez/internal/api"
)

// TestRoutinesKeys_RunAndDisabledRow covers the two dispatch decisions the
// panel makes: `R` asks the daemon for the list, and `r` runs the row under
// the cursor unless the project disabled it.
func TestRoutinesKeys_RunAndDisabledRow(t *testing.T) {
	t.Parallel()

	fc := &fakeClient{}
	m := New(Options{NoColor: true})
	m.client = fc

	sized, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	m, ok := sized.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", sized)
	}

	m = applyKey(t, m, "R")

	if fc.listed != 1 {
		t.Fatalf("listed = %d, want the panel to request routines on open", fc.listed)
	}

	m.routines = []api.RoutineInfo{
		{Name: "nightly", Enabled: true},
		{Name: "gate-format", Enabled: false},
	}

	m = applyKey(t, m, "r")

	if len(fc.ranRoutines) != 1 || fc.ranRoutines[0] != "nightly" {
		t.Fatalf("ranRoutines = %v, want the row under the cursor", fc.ranRoutines)
	}

	m = applyKey(t, m, "j")
	m = applyKey(t, m, "r")

	if len(fc.ranRoutines) != 1 {
		t.Errorf("ranRoutines = %v, want a disabled routine not to run", fc.ranRoutines)
	}
	if m.status == "" {
		t.Error("a disabled routine must say why it did not run")
	}
}

func applyKey(t *testing.T, m Model, key string) Model {
	t.Helper()

	next, _ := m.Update(tea.KeyPressMsg{Code: rune(key[0]), Text: key})

	out, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", next)
	}

	return out
}
