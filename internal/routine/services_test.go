package routine_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/routine"
)

// A service exists because it is expensive, so the count is the point: two
// routines wanting the same database must not start it twice, and the first
// to finish must not take it from the second.
func TestServices_StartsOnceAndStopsWhenTheLastHolderLeaves(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	ups := filepath.Join(dir, "up")
	downs := filepath.Join(dir, "down")

	services := routine.NewServices([]routine.ServiceDef{{
		Name: "db",
		Up:   []string{"sh", "-c", "echo x >> " + ups},
		Down: []string{"sh", "-c", "echo x >> " + downs},
		Dir:  dir,
	}})

	if err := services.Up(t.Context(), "db"); err != nil {
		t.Fatalf("first Up: %v", err)
	}

	if err := services.Up(t.Context(), "db"); err != nil {
		t.Fatalf("second Up: %v", err)
	}

	if got := lines(t, ups); got != 1 {
		t.Errorf("started %d times, want once for two holders", got)
	}

	if err := services.Down(t.Context(), "db"); err != nil {
		t.Fatalf("first Down: %v", err)
	}

	if got := lines(t, downs); got != 0 {
		t.Fatalf("stopped while another holder had it")
	}

	if err := services.Down(t.Context(), "db"); err != nil {
		t.Fatalf("second Down: %v", err)
	}

	if got := lines(t, downs); got != 1 {
		t.Errorf("stopped %d times after the last holder left, want once", got)
	}
}

// A service that never becomes ready fails the step that wanted it, rather
// than leaving the next step to fail for a reason it cannot explain.
func TestServices_FailsWhenItNeverBecomesReady(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	services := routine.NewServices([]routine.ServiceDef{{
		Name:      "never",
		Up:        []string{"true"},
		Ready:     []string{"false"},
		Dir:       dir,
		ReadyWait: 40 * time.Millisecond,
		Timeout:   2 * time.Second,
	}})

	err := services.Up(t.Context(), "never")
	if err == nil {
		t.Fatal("Up returned nil, want the readiness failure")
	}

	// The hold is released, so a later caller starts it rather than finding
	// it already claimed by the attempt that failed.
	if err := services.Up(t.Context(), "never"); err == nil {
		t.Error("a second Up succeeded, so the failed one kept its hold")
	}
}

// A step naming a service the project never declared is a config-load
// failure, not a run-time one: that is the whole reason Bind exists.
func TestServiceActions_RefuseAnUndeclaredName(t *testing.T) {
	t.Parallel()

	actions := routine.ServiceActions(routine.NewServices([]routine.ServiceDef{{Name: "db"}}))
	if len(actions) != 2 {
		t.Fatalf("built %d actions, want up and down", len(actions))
	}

	for _, a := range actions {
		if _, err := a.Bind(map[string]any{"name": "nope"}); err == nil {
			t.Errorf("%s bound an undeclared service", a.Name)
		}

		if _, err := a.Bind(map[string]any{"name": "db"}); err != nil {
			t.Errorf("%s refused a declared service: %v", a.Name, err)
		}
	}
}

// lines counts the lines a fixture command appended, zero when it never ran.
func lines(t *testing.T, path string) int {
	t.Helper()

	body, err := os.ReadFile(path) //nolint:gosec // the path is this test's own temp dir
	if err != nil {
		return 0
	}

	return len(strings.Fields(string(body)))
}
