package finish

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/kyleking/wavez/internal/codeintel"
	"github.com/kyleking/wavez/internal/tool"
)

// Coverage answers which tests execute a line range. It is the coverage map
// the index already builds, so this check costs a query rather than a run.
type Coverage interface {
	CoveringTests(ctx context.Context, file string, start, end int) ([]codeintel.CoverageTest, error)
}

// untested are the extensions a coverage map never has anything to say
// about. A change to one of them abstains rather than failing, since
// "no test executes this Markdown" is true and useless.
var untested = map[string]bool{
	".golden": true, ".json": true, ".md": true, ".pkl": true, ".sql": true,
	".toml": true, ".txt": true, ".yaml": true, ".yml": true,
}

// ChangedLinesAreTested reports the changed ranges no test executes.
//
// It abstains for a change with no line ranges, which is what a whole-file
// write produces, and for files a coverage map cannot speak about. It also
// abstains entirely when nothing in the change set is covered, because a
// workspace whose coverage map was never built would otherwise fail every
// run for a reason that has nothing to do with the run.
func ChangedLinesAreTested(ctx context.Context, changes []tool.Change, cov Coverage) (Report, error) {
	if cov == nil {
		return Report{}, nil
	}

	var (
		uncovered []string
		covered   int
	)

	for _, c := range changes {
		if untested[strings.ToLower(path.Ext(c.Path))] {
			continue
		}

		for _, r := range c.Ranges {
			tests, err := cov.CoveringTests(ctx, c.Path, r.Start, r.End)
			if err != nil {
				return Report{}, fmt.Errorf("reading coverage for %s: %w", c.Path, err)
			}

			if len(tests) > 0 {
				covered++

				continue
			}

			uncovered = append(uncovered, fmt.Sprintf("%s:%d-%d", c.Path, r.Start, r.End))
		}
	}

	if covered == 0 || len(uncovered) == 0 {
		return Report{}, nil
	}

	return Report{Findings: []Finding{{
		Check:  "no test executes these changed lines",
		Detail: strings.Join(uncovered, ", "),
	}}}, nil
}
