package gate_test

import (
	"testing"

	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/tool"
)

// TestAttributable covers the fork that decides whether a failing gate
// reaches the model or the scheduler. The reachability case is the one worth
// the graph: a run that deletes a function breaks a caller in a file it never
// opened, and that failure is still its own.
func TestAttributable(t *testing.T) {
	t.Parallel()

	graph := gate.NewImportGraph(
		map[string]string{
			"internal/runtime/server.go":     "x/internal/runtime",
			"internal/router/router.go":      "x/internal/router",
			"internal/router/router_test.go": "x/internal/router",
			"cmd/wavezd/main_test.go":        "x/cmd/wavezd",
			"internal/daemon/daemon.go":      "x/internal/daemon",
		},
		map[string][]string{
			"x/internal/runtime": {"x/internal/router"},
			"x/internal/daemon":  {"x/cmd/wavezd"},
		},
	)

	tests := []struct {
		graph   *gate.ImportGraph
		name    string
		changes []tool.Change
		result  gate.Result
		want    bool
	}{
		{
			name: "a frame naming a changed file is the run's",
			result: gate.Result{Failures: []gate.TrimmedFailure{
				{Test: "TestX", Frames: []string{"server.go:10: boom"}},
			}},
			graph:   graph,
			changes: []tool.Change{{Path: "internal/runtime/server.go"}},
			want:    true,
		},
		{
			name: "an importer of a changed package is the run's",
			result: gate.Result{Failures: []gate.TrimmedFailure{
				{Test: "build", Context: []string{"internal/router/router.go:14: undefined: Serve"}},
			}},
			graph:   graph,
			changes: []tool.Change{{Path: "internal/runtime/server.go"}},
			want:    true,
		},
		{
			name: "a package no change reaches belongs to the tree",
			result: gate.Result{Failures: []gate.TrimmedFailure{
				{Test: "TestServe", Context: []string{"main_test.go:27: occupying socket: bind: invalid argument"}},
			}},
			graph:   graph,
			changes: []tool.Change{{Path: "internal/runtime/server.go"}},
			want:    false,
		},
		{
			name: "no graph rules nothing out",
			result: gate.Result{Failures: []gate.TrimmedFailure{
				{Test: "TestServe", Context: []string{"main_test.go:27: boom"}},
			}},
			changes: []tool.Change{{Path: "internal/runtime/server.go"}},
			want:    true,
		},
		{
			name: "a changed file the graph never listed rules nothing out",
			result: gate.Result{Failures: []gate.TrimmedFailure{
				{Test: "TestServe", Context: []string{"main_test.go:27: boom"}},
			}},
			graph:   graph,
			changes: []tool.Change{{Path: "internal/brand/new.go"}},
			want:    true,
		},
		{
			name:    "a passing result is nobody's failure",
			result:  gate.Result{Pass: true},
			graph:   graph,
			changes: []tool.Change{{Path: "internal/runtime/server.go"}},
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := gate.Attributable(tt.result, tt.graph, tt.changes); got != tt.want {
				t.Errorf("Attributable = %v, want %v", got, tt.want)
			}
		})
	}
}
