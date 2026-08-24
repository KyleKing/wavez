package gate

import (
	"context"
	"fmt"
	"path"
	"sort"

	"github.com/kyleking/wavez/internal/codeintel"
	"github.com/kyleking/wavez/internal/tool"
)

// LineCoverage is the coverage lookup Select needs. A codeintel.Store
// satisfies it directly.
type LineCoverage interface {
	CoveringTests(ctx context.Context, file string, start, end int) ([]codeintel.CoverageTest, error)
}

// CoverageMap is a LineCoverage that also reports whether its map is
// finished. Selection needs that distinction because a map still building
// answers "no test covers this range" for every test it has not run yet,
// and three tests chosen from a half-built map is a wrong answer where
// falling back to importer level is merely a coarse one. A cov that
// implements only LineCoverage is read as a finished map.
type CoverageMap interface {
	LineCoverage
	CoverageReady() bool
}

// Select resolves changes to the narrowest tier DESIGN.md's Gates section
// orders: line-to-test where the coverage map covers every changed range,
// transitive importers of the changed files' packages otherwise, and the
// changed files' own packages as the last fallback. Each tier is tried only
// once the tier above it could not decide for the whole batch, so a run
// never mixes a line-level answer for one file with an importer-level
// answer for another. The model is never consulted.
//
// A nil cov or graph means that tier has nothing to say, not that it
// decided: selection drops to the next tier down, ending at the changed
// files' own packages.
func Select(ctx context.Context, cov LineCoverage, graph *ImportGraph, changes []tool.Change) (Selection, error) {
	tests, ok, err := selectByLine(ctx, cov, changes)
	if err != nil {
		return Selection{}, err
	}
	if ok {
		return Selection{Level: LevelLine, Tests: tests}, nil
	}

	if graph != nil {
		if pkgs, ok := selectByImporters(graph, changes); ok {
			return Selection{Level: LevelImporter, Packages: pkgs}, nil
		}
	}

	return Selection{Level: LevelPackage, Packages: fallbackPackages(graph, changes)}, nil
}

// selectByLine reports ok only when every changed range in every changed
// file resolves to at least one covering test; a single unresolved range
// means the batch falls through to the importer tier rather than mixing
// selection granularity within one run. A map that reports itself unready
// is not queried at all, since its silence carries no information.
func selectByLine(ctx context.Context, cov LineCoverage, changes []tool.Change) ([]string, bool, error) {
	if !coverageUsable(cov) || len(changes) == 0 {
		return nil, false, nil
	}

	seen := make(map[string]struct{})

	var tests []string

	for _, ch := range changes {
		if len(ch.Ranges) == 0 {
			return nil, false, nil
		}

		for _, r := range ch.Ranges {
			covering, err := cov.CoveringTests(ctx, ch.Path, r.Start, r.End)
			if err != nil {
				return nil, false, fmt.Errorf("selecting line coverage for %s:%d-%d: %w", ch.Path, r.Start, r.End, err)
			}
			if len(covering) == 0 {
				return nil, false, nil
			}

			collect(seen, covering, &tests)
		}
	}

	sort.Strings(tests)

	return tests, true, nil
}

func coverageUsable(cov LineCoverage) bool {
	if cov == nil {
		return false
	}

	m, ok := cov.(CoverageMap)

	return !ok || m.CoverageReady()
}

func collect(seen map[string]struct{}, covering []codeintel.CoverageTest, tests *[]string) {
	for _, t := range covering {
		if _, ok := seen[t.TestID]; ok {
			continue
		}

		seen[t.TestID] = struct{}{}
		*tests = append(*tests, t.TestID)
	}
}

// selectByImporters reports ok only when every changed file resolves to a
// known package in graph; an unresolved file (new file the graph has not
// seen, or a non-Go file) means the importer tier cannot decide either.
func selectByImporters(graph *ImportGraph, changes []tool.Change) ([]string, bool) {
	roots := make(map[string]struct{})

	for _, ch := range changes {
		pkg, ok := graph.FilePackage[ch.Path]
		if !ok {
			return nil, false
		}

		roots[pkg] = struct{}{}
	}

	result := make(map[string]struct{})
	for pkg := range roots {
		result[pkg] = struct{}{}
		for _, importer := range graph.transitiveImporters(pkg) {
			result[importer] = struct{}{}
		}
	}

	return sortedKeys(result), true
}

// fallbackPackages returns the changed files' own packages: graph's
// FilePackage entry where known, otherwise the file's directory as a
// last-resort package guess.
//
// The directory guess is spelled as a relative pattern, because `go test`
// reads a bare `internal/guard` as a standard-library package and fails the
// gate with "not in std". The graph answers for every file it has seen, so
// this path is reached exactly when a change creates a file, and a gate that
// reports "your new file broke the package" is worse than one that says
// nothing: measured on `h5`, one correct `move` call drew an unattributable
// build failure and the run spent fourteen of fifteen turns chasing it.
func fallbackPackages(graph *ImportGraph, changes []tool.Change) []string {
	seen := make(map[string]struct{})

	for _, ch := range changes {
		pkg := relativePattern(path.Dir(ch.Path))
		if graph != nil {
			if p, ok := graph.FilePackage[ch.Path]; ok {
				pkg = p
			}
		}

		seen[pkg] = struct{}{}
	}

	return sortedKeys(seen)
}

// relativePattern makes a repository-relative directory into the pattern
// `go test` reads as a directory rather than as an import path.
func relativePattern(dir string) string {
	// A file at the repository root widens to the module rather than to
	// `./.`, which fails outright on a root holding no Go files of its own.
	if dir == "" || dir == "." {
		return "./..."
	}

	return "./" + dir
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	sort.Strings(out)

	return out
}
