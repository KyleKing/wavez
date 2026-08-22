package gate_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/kyleking/wavez/internal/codeintel"
	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/tool"
)

type fakeLineCoverage map[string][]codeintel.CoverageTest

func (f fakeLineCoverage) CoveringTests(
	_ context.Context, file string, start, end int,
) ([]codeintel.CoverageTest, error) {
	return f[coverageKey(file, start, end)], nil
}

func coverageKey(file string, start, end int) string {
	return file + ":" + strconv.Itoa(start) + "-" + strconv.Itoa(end)
}

// unreadyCoverage is a coverage map still building: it answers queries but
// reports itself unready, which is what a CoverageAdapter does before its
// first full build finishes.
type unreadyCoverage struct{ fakeLineCoverage }

func (unreadyCoverage) CoverageReady() bool { return false }

func TestSelect(t *testing.T) {
	t.Parallel()

	change := func(path string, start, end int) tool.Change {
		return tool.Change{Path: path, Ranges: []tool.LineRange{{Start: start, End: end}}}
	}

	tests := []struct {
		name      string
		cov       gate.LineCoverage
		graph     *gate.ImportGraph
		changes   []tool.Change
		wantLevel gate.Level
		wantTests []string
		wantPkgs  []string
	}{
		{
			name: "line level when coverage map covers every changed range",
			cov: fakeLineCoverage{
				coverageKey("pkg/a.go", 1, 5): {{TestID: "pkg.TestA", TestHash: "h1"}},
			},
			changes:   []tool.Change{change("pkg/a.go", 1, 5)},
			wantLevel: gate.LevelLine,
			wantTests: []string{"pkg.TestA"},
		},
		{
			name: "importer level when line coverage is empty but the graph knows the file",
			cov:  fakeLineCoverage{},
			graph: gate.NewImportGraph(
				map[string]string{"pkg/a.go": "example.com/pkg"},
				map[string][]string{"example.com/pkg": {"example.com/consumer"}},
			),
			changes:   []tool.Change{change("pkg/a.go", 1, 5)},
			wantLevel: gate.LevelImporter,
			wantPkgs:  []string{"example.com/consumer", "example.com/pkg"},
		},
		{
			name: "an unready map is not consulted, however much coverage it holds",
			cov: unreadyCoverage{fakeLineCoverage{
				coverageKey("pkg/a.go", 1, 5): {{TestID: "pkg.TestA", TestHash: "h1"}},
			}},
			graph: gate.NewImportGraph(
				map[string]string{"pkg/a.go": "example.com/pkg"},
				map[string][]string{"example.com/pkg": {"example.com/consumer"}},
			),
			changes:   []tool.Change{change("pkg/a.go", 1, 5)},
			wantLevel: gate.LevelImporter,
			wantPkgs:  []string{"example.com/consumer", "example.com/pkg"},
		},
		{
			// The directory guess is spelled relative because `go test pkg`
			// reads pkg as a standard-library package and fails the gate
			// with "not in std", which is what a change that creates a file
			// used to draw: the graph answers for every file it has seen,
			// so a new file is exactly what reaches this path.
			name:      "package level when neither line coverage nor the graph can decide",
			cov:       fakeLineCoverage{},
			graph:     nil,
			changes:   []tool.Change{change("pkg/a.go", 1, 5)},
			wantLevel: gate.LevelPackage,
			wantPkgs:  []string{"./pkg"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := gate.Select(context.Background(), tt.cov, tt.graph, tt.changes)
			if err != nil {
				t.Fatalf("Select: %v", err)
			}
			if got.Level != tt.wantLevel {
				t.Errorf("Level = %q, want %q", got.Level, tt.wantLevel)
			}
			if !equalStrings(got.Tests, tt.wantTests) {
				t.Errorf("Tests = %v, want %v", got.Tests, tt.wantTests)
			}
			if !equalStrings(got.Packages, tt.wantPkgs) {
				t.Errorf("Packages = %v, want %v", got.Packages, tt.wantPkgs)
			}
		})
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

// A caller with no coverage map at all (a one-off check outside the daemon)
// must get the next tier down, not a panic.
func TestSelectWithoutCoverageFallsThrough(t *testing.T) {
	t.Parallel()

	sel, err := gate.Select(context.Background(), nil, nil, []tool.Change{
		{Path: "internal/gate/select.go", Ranges: []tool.LineRange{{Start: 1, End: 2}}},
	})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}

	if sel.Level != gate.LevelPackage {
		t.Errorf("Level = %q, want %q", sel.Level, gate.LevelPackage)
	}
}
