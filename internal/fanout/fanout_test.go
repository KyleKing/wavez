package fanout_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/fanout"
)

// Fanning out over a list nobody proved disjoint is how two lanes edit one
// file, so overlap decides the split and the requested count does not.
func TestSplitKeepsOverlappingWorkInOneLane(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		tasks []fanout.Task
		want  int
	}{
		{
			name: "a shared file",
			tasks: []fanout.Task{
				{Name: "a", Paths: []string{"pkg/one.go"}},
				{Name: "b", Paths: []string{"pkg/one.go", "pkg/two.go"}},
				{Name: "c", Paths: []string{"other/three.go"}},
			},
			want: 2,
		},
		{
			name: "a directory holding another lane's file",
			tasks: []fanout.Task{
				{Name: "a", Paths: []string{"pkg"}},
				{Name: "b", Paths: []string{"pkg/two.go"}},
				{Name: "c", Paths: []string{"other/three.go"}},
			},
			want: 2,
		},
		{
			name: "a chain joined through a middle task",
			tasks: []fanout.Task{
				{Name: "a", Paths: []string{"one.go"}},
				{Name: "b", Paths: []string{"one.go", "two.go"}},
				{Name: "c", Paths: []string{"two.go"}},
			},
			want: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			lanes, err := fanout.Split(tc.tasks, 8)
			if err != nil {
				t.Fatalf("Split: %v", err)
			}
			if len(lanes) != tc.want {
				t.Errorf("got %d lanes, want %d: %v", len(lanes), tc.want, lanes)
			}
			if err := fanout.Check(lanes); err != nil {
				t.Errorf("Check: %v", err)
			}
		})
	}
}

func TestSplitRefusesATaskItCannotFence(t *testing.T) {
	t.Parallel()

	_, err := fanout.Split([]fanout.Task{
		{Name: "tidy the docs", Paths: []string{"docs/x.md"}},
		{Name: "make it faster"},
	}, 4)

	if !errors.Is(err, fanout.ErrUnscoped) {
		t.Errorf("Split err = %v, want ErrUnscoped", err)
	}
}

// A lane holding the file with 200 findings must not finish last while the
// others idle, so the packing balances weight rather than task count.
func TestSplitBalancesByWeight(t *testing.T) {
	t.Parallel()

	lanes, err := fanout.Split([]fanout.Task{
		{Name: "heavy", Paths: []string{"a.py"}, Weight: 100},
		{Name: "light1", Paths: []string{"b.py"}, Weight: 40},
		{Name: "light2", Paths: []string{"c.py"}, Weight: 40},
		{Name: "light3", Paths: []string{"d.py"}, Weight: 30},
	}, 2)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}

	if len(lanes) != 2 {
		t.Fatalf("got %d lanes, want 2", len(lanes))
	}
	if err := fanout.Check(lanes); err != nil {
		t.Fatalf("Check: %v", err)
	}

	heavy, light := lanes[0].Weight, lanes[1].Weight
	if heavy < light {
		heavy, light = light, heavy
	}

	if heavy != 110 || light != 100 {
		t.Errorf("lane weights %d and %d, want 110 and 100", heavy, light)
	}
}

// Check is the half a caller other than Split needs: a plan a model wrote,
// or one written by hand, is verified rather than believed.
func TestCheckNamesTheLanesThatCollide(t *testing.T) {
	t.Parallel()

	err := fanout.Check([]fanout.Lane{
		{Paths: []string{"internal/tui"}},
		{Paths: []string{"internal/app/app.go"}},
		{Paths: []string{"internal/tui/home.go"}},
	})
	if err == nil {
		t.Fatal("Check accepted a lane writing inside another lane's directory")
	}

	if !strings.Contains(err.Error(), "internal/tui") ||
		!strings.Contains(err.Error(), "internal/tui/home.go") {
		t.Errorf("Check error does not name both paths: %v", err)
	}
}

// The real ruff output the 2026-09-03 lane read as 40 windowed lines: 349
// findings that the harness can now turn into a split it verified.
func TestFromFindingsPartitionsRealCheckOutput(t *testing.T) {
	t.Parallel()

	output, err := os.ReadFile("../reduce/testdata/ruff_check.txt")
	if err != nil {
		t.Fatalf("reading the fixture: %v", err)
	}

	tasks := fanout.FromFindings(string(output))
	if len(tasks) < 2 {
		t.Fatalf("got %d tasks, want one per file with findings", len(tasks))
	}

	total := 0
	for _, task := range tasks {
		total += task.Weight
	}

	if total != 349 {
		t.Errorf("tasks carry %d findings, want the 349 the check reported", total)
	}

	lanes, err := fanout.Split(tasks, 4)
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if len(lanes) != 4 {
		t.Errorf("got %d lanes, want 4 from %d files", len(lanes), len(tasks))
	}
	if err := fanout.Check(lanes); err != nil {
		t.Errorf("Check: %v", err)
	}

	// Every file with a finding reaches exactly one lane. A file in none is
	// work the split dropped, and a file in two is the failure this package
	// exists to prevent.
	for _, task := range tasks {
		if n := lanesHolding(lanes, task.Paths[0]); n != 1 {
			t.Errorf("%s is in %d lanes, want 1", task.Paths[0], n)
		}
	}
}

func lanesHolding(lanes []fanout.Lane, path string) int {
	n := 0

	for i := range lanes {
		for _, p := range lanes[i].Paths {
			if p == path {
				n++

				break
			}
		}
	}

	return n
}
