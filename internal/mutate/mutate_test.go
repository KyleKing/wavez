package mutate_test

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/mutate"
	"github.com/kyleking/wavez/internal/tool"
)

const sample = `package sample

func Grade(score int) string {
	if score >= 90 {
		return "A"
	}
	if score == 50 {
		return "pass"
	}

	return "F"
}

func Always() bool { return true }
`

func TestMutantsCoverEveryOperatorAndStayCompilable(t *testing.T) {
	t.Parallel()

	mutants, err := mutate.Mutants("sample.go", []byte(sample), nil)
	if err != nil {
		t.Fatalf("Mutants: %v", err)
	}

	want := map[string]string{
		mutate.OpBoundary: ">= -> >",
		mutate.OpNegate:   "== -> !=",
		mutate.OpBool:     "true -> false",
	}

	seen := map[string]bool{}

	for _, m := range mutants {
		seen[m.Op] = true

		if got := m.Before + " -> " + m.After; got != want[m.Op] {
			t.Errorf("%s: %s, want %s", m.Op, got, want[m.Op])
		}

		// A mutant that does not parse is indistinguishable from a killed
		// one when the suite runs, so the operator set must never emit one.
		if _, err := parser.ParseFile(token.NewFileSet(), m.Path, m.Source, parser.SkipObjectResolution); err != nil {
			t.Errorf("%s does not parse: %v", m.Describe(), err)
		}

		if strings.Count(string(m.Source), "\n") != strings.Count(sample, "\n") {
			t.Errorf("%s changed the line count, so its reported line is wrong", m.Describe())
		}
	}

	for op := range want {
		if !seen[op] {
			t.Errorf("no %s mutant produced", op)
		}
	}
}

func TestMutantsRespectChangedRanges(t *testing.T) {
	t.Parallel()

	// Line 4 holds the >= comparison; nothing else in the file may be
	// mutated when only that line changed.
	mutants, err := mutate.Mutants("sample.go", []byte(sample), []tool.LineRange{{Start: 4, End: 4}})
	if err != nil {
		t.Fatalf("Mutants: %v", err)
	}

	if len(mutants) != 1 {
		t.Fatalf("len = %d, want 1: %v", len(mutants), describeAll(mutants))
	}

	if mutants[0].Line != 4 || mutants[0].Op != mutate.OpBoundary {
		t.Errorf("got %s, want a boundary mutant on line 4", mutants[0].Describe())
	}
}

func describeAll(mutants []mutate.Mutant) []string {
	out := make([]string, 0, len(mutants))
	for _, m := range mutants {
		out = append(out, m.Describe())
	}

	return out
}
