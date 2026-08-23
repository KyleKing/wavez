package finish_test

import (
	"context"
	"testing"

	"github.com/kyleking/wavez/internal/codeintel"
	"github.com/kyleking/wavez/internal/finish"
	"github.com/kyleking/wavez/internal/tool"
)

// stubCoverage says which files it has coverage for.
type stubCoverage map[string]bool

func (s stubCoverage) CoveringTests(
	_ context.Context, file string, _, _ int,
) ([]codeintel.CoverageTest, error) {
	if !s[file] {
		return nil, nil
	}

	return []codeintel.CoverageTest{{TestID: "TestThing"}}, nil
}

// The bound is that a run leaving changed lines no test executes has not
// finished. It abstains rather than failing whenever the map has nothing to
// say, because a workspace with no coverage built would otherwise fail
// every run for the workspace's reason and not the run's.
func TestChangedLinesAreTested(t *testing.T) {
	t.Parallel()

	rng := []tool.LineRange{{Start: 10, End: 14}}

	tests := []struct {
		cov     stubCoverage
		name    string
		changes []tool.Change
		wantOK  bool
	}{
		{
			name:    "an untested change beside a tested one is named",
			changes: []tool.Change{{Path: "a.go", Ranges: rng}, {Path: "b.go", Ranges: rng}},
			cov:     stubCoverage{"a.go": true},
		},
		{
			name:    "a change set the map knows nothing about abstains",
			changes: []tool.Change{{Path: "a.go", Ranges: rng}},
			cov:     stubCoverage{},
			wantOK:  true,
		},
		{
			name:    "a doc change beside a covered one does not fail",
			changes: []tool.Change{{Path: "a.go", Ranges: rng}, {Path: "DESIGN.md", Ranges: rng}},
			cov:     stubCoverage{"a.go": true},
			wantOK:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			report, err := finish.ChangedLinesAreTested(t.Context(), tt.changes, tt.cov)
			if err != nil {
				t.Fatalf("ChangedLinesAreTested: %v", err)
			}

			if report.OK() != tt.wantOK {
				t.Fatalf("OK() = %v, want %v:\n%s", report.OK(), tt.wantOK, report)
			}
		})
	}
}
