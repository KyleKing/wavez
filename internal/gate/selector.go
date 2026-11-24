package gate

import (
	"context"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// Selector picks the tests a change can affect, so that the test check runs
// those and not every test of every package the change reaches through its
// importers. A runner without one runs every test of those packages.
type Selector interface {
	// Select answers for the change from base to the working tree under
	// root. Base is the commit or tree object the session started from,
	// and "" outside a session, where the selector diffs against HEAD
	// itself. Changed holds the root-relative paths the runner selected
	// its checks with.
	Select(ctx context.Context, root, base string, changed []string) (Selection, error)
}

// Selection is the tests a Selector picked.
type Selection struct {
	// Tests maps a package, as the ./-prefixed pattern go takes, to the
	// names of the top-level tests in it to run. A package that is not a
	// key has none of its tests run or compiled.
	Tests map[string][]string
	// Complete is true when Tests holds every test the change can affect.
	// A selection that is not, or a Select that failed, leaves the test
	// check as it is without a selector: a test selection that may have
	// missed one is never allowed to pass a change on its word.
	Complete bool
}

// WithSelector makes the test check run what s selects.
func WithSelector(s Selector) Option {
	return func(r *runner) { r.selector = s }
}

// testPlan is the packages `go test` is handed and the -run pattern over
// them, "" for every test.
type testPlan struct {
	run  string
	pkgs []string
}

// planTests asks the selector, if there is one, which tests to run, and
// falls back to every test of all when it cannot say for certain. A name
// selected in one package also runs where another package has a test of the
// same name, which runs more and never less.
func (r *runner) planTests(ctx context.Context, proj project, all []string) testPlan {
	if r.selector == nil {
		return testPlan{pkgs: all}
	}

	base := ""
	if b, ok := baselineFrom(ctx); ok {
		base = b.ref()
	}

	sel, err := r.selector.Select(ctx, proj.root, base, proj.changed)
	if err != nil || !sel.Complete {
		return testPlan{pkgs: all}
	}

	var (
		pkgs  []string
		names []string
	)

	for _, pkg := range slices.Sorted(maps.Keys(sel.Tests)) {
		if len(sel.Tests[pkg]) == 0 {
			continue
		}

		pkgs = append(pkgs, pkg)

		for _, name := range sel.Tests[pkg] {
			if quoted := regexp.QuoteMeta(name); !slices.Contains(names, quoted) {
				names = append(names, quoted)
			}
		}
	}

	if len(pkgs) == 0 {
		return testPlan{}
	}

	slices.Sort(names)

	return testPlan{pkgs: pkgs, run: "^(" + strings.Join(names, "|") + ")$"}
}
