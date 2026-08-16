package gate_test

import (
	"testing"

	"github.com/kyleking/wavez/internal/gate"
)

// blastGraph is a diamond plus an unrelated package: app imports api and
// cli, both of which import core, and lone imports nothing.
//
//	core ← api ← app
//	  ↑          ↑
//	  └── cli ───┘
func blastGraph() *gate.ImportGraph {
	return gate.NewImportGraph(
		map[string]string{
			"core/core.go": "m/core",
			"api/api.go":   "m/api",
			"cli/cli.go":   "m/cli",
			"app/app.go":   "m/app",
			"lone/lone.go": "m/lone",
		},
		map[string][]string{
			"m/core": {"m/api", "m/cli"},
			"m/api":  {"m/app"},
			"m/cli":  {"m/app"},
		},
	)
}

func TestImportGraph_BlastRadius(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		graph     *gate.ImportGraph
		paths     []string
		want      int
		wantKnown bool
	}{
		{
			name:  "counts transitive importers once across a diamond",
			graph: blastGraph(), paths: []string{"core/core.go"}, want: 3, wantKnown: true,
		},
		{
			name:  "a leaf package reaches nothing",
			graph: blastGraph(), paths: []string{"app/app.go"}, want: 0, wantKnown: true,
		},
		{
			name:  "changed packages are excluded from their own radius",
			graph: blastGraph(), paths: []string{"core/core.go", "api/api.go"}, want: 2, wantKnown: true,
		},
		{
			name:  "an unknown path leaves the signal uncomputed, not zero",
			graph: blastGraph(), paths: []string{"README.md"}, want: 0, wantKnown: false,
		},
		{
			name:  "one known path among unknown ones still counts",
			graph: blastGraph(), paths: []string{"README.md", "core/core.go"}, want: 3, wantKnown: true,
		},
		{name: "no paths", graph: blastGraph(), paths: nil, want: 0, wantKnown: false},
		{name: "nil graph", graph: nil, paths: []string{"core/core.go"}, want: 0, wantKnown: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, known := tt.graph.BlastRadius(tt.paths)
			if got != tt.want || known != tt.wantKnown {
				t.Errorf("BlastRadius(%v) = %d, %v; want %d, %v", tt.paths, got, known, tt.want, tt.wantKnown)
			}
		})
	}
}
