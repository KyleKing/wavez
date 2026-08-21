package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/tool"
	"github.com/kyleking/wavez/internal/vcs"
)

// errMutantsSurvived reports a mutation run that finished with work left
// undone: a mutant the tests ignored, or a mutant the run never got to.
var errMutantsSurvived = errors.New("mutants survived")

// mutationCheck mutates the lines changed in the working copy and reports
// every mutant the selected tests failed to kill.
func mutationCheck(ctx context.Context, root string) error {
	jj := vcs.NewJj()

	diff, err := jj.WorkingCopyDiff(ctx, root)
	if err != nil {
		return fmt.Errorf("reading the working-copy diff: %w", err)
	}

	changes := goChangesFromDiff(vcs.ChangedRanges(diff))
	if len(changes) == 0 {
		fmt.Fprintln(os.Stderr, "no changed Go lines to mutate")

		return nil
	}

	graph, err := gate.BuildImportGraph(ctx, root)
	if err != nil {
		return fmt.Errorf("building the import graph: %w", err)
	}

	selection, err := gate.Select(ctx, nil, graph, changes)
	if err != nil {
		return fmt.Errorf("selecting tests: %w", err)
	}

	result, err := gate.NewMutationGate(root, jj).Run(ctx, gate.RunContext{
		RepoRoot: root, Changes: changes, Selection: selection,
	})
	if err != nil {
		return fmt.Errorf("running the mutation gate: %w", err)
	}

	return reportMutation(result, changes)
}

func reportMutation(result gate.Result, changes []tool.Change) error {
	fmt.Fprintf(os.Stderr, "mutation: %d mutant(s) across %d file(s) at %s level\n",
		result.Examined, len(changes), result.Level)

	findings := append(append([]gate.TrimmedFailure(nil), result.Failures...), result.Advisories...)
	if len(findings) == 0 {
		fmt.Fprintln(os.Stderr, "every mutant was killed")

		return nil
	}

	for _, f := range findings {
		for _, frame := range f.Frames {
			fmt.Fprintf(os.Stderr, "  %s: %s\n", f.Test, frame)
		}
	}

	return errMutantsSurvived
}

// goChangesFromDiff narrows parsed diff ranges to Go files, dropping tests:
// mutating a test file asks whether the tests test themselves, which is not
// the question.
func goChangesFromDiff(ranges map[string][]tool.LineRange) []tool.Change {
	paths := make([]string, 0, len(ranges))

	for path := range ranges {
		if filepath.Ext(path) == ".go" && !isGoTestFile(path) {
			paths = append(paths, path)
		}
	}

	sort.Strings(paths)

	out := make([]tool.Change, 0, len(paths))
	for _, path := range paths {
		out = append(out, tool.Change{Path: path, Ranges: ranges[path]})
	}

	return out
}

func isGoTestFile(path string) bool {
	base := filepath.Base(path)

	return len(base) > len("_test.go") && base[len(base)-len("_test.go"):] == "_test.go"
}
