package cycle

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"sort"
	"strings"

	"github.com/kyleking/wavez/internal/astgrep"
	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/tool"
)

// DefaultSweepLanguage is the language a sweep assumes when a phase names
// none.
const DefaultSweepLanguage = "go"

// GoProber re-runs the Go tests a change set declares on the tree as it
// stands. Only tests are probed: an artifact the harness re-runs has to be
// something it can run itself, and running a command the model wrote would
// put model text where a measurement belongs.
type GoProber struct{}

// NewGoProber builds a Prober over `go test`.
func NewGoProber() GoProber { return GoProber{} }

// Probe runs the test functions declared on the change set's changed test
// lines and reports which of them fail. A tree that does not build fails
// every test it was asked about, since none of them can pass there.
func (GoProber) Probe(ctx context.Context, repoRoot string, changes []tool.Change) ([]Observation, error) {
	tests, err := gate.ChangedTests(repoRoot, changes)
	if err != nil {
		return nil, fmt.Errorf("reading the change set's tests: %w", err)
	}

	if len(tests) == 0 {
		return nil, nil
	}

	targets := testTargets(tests)

	run := "^(" + strings.Join(targets.names, "|") + ")$"
	args := append([]string{"test", "-count=1", "-json", "-run", run}, targets.packages...)

	//nolint:gosec // args are test names and package paths derived from this cycle's own change set
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = repoRoot

	out, _ := cmd.Output() //nolint:errcheck // status is carried by the parsed summary, not this call's error

	summary, err := gate.ParseGoTestJSON(bytes.NewReader(out))
	if err != nil {
		return nil, fmt.Errorf("parsing go test output: %w", err)
	}

	return observe(tests, summary), nil
}

// observe pairs each test the change set declares with what the run showed.
// A test the run never reported on is a failure to observe rather than a
// pass, so it is reported as failed with that reason.
func observe(tests []gate.TestFunc, summary gate.GoTestSummary) []Observation {
	passed := make(map[string]struct{}, len(summary.PassedTests))
	for _, id := range summary.PassedTests {
		passed[shortName(id)] = struct{}{}
	}

	failed := make(map[string]string, len(summary.FailedTests))
	for _, f := range summary.FailedTests {
		failed[f.Name] = strings.Join(f.Output, "\n")
	}

	out := make([]Observation, 0, len(tests))

	for _, t := range tests {
		obs := Observation{Package: t.Package, Test: t.Name}

		switch {
		case summary.BuildFailed:
			obs.Failed, obs.Detail = true, "the package does not build"
		case hasKey(failed, t.Name):
			obs.Failed, obs.Detail = true, failed[t.Name]
		case hasKey(passed, t.Name):
			obs.Detail = "passed"
		default:
			obs.Failed, obs.Detail = true, "the test did not run"
		}

		out = append(out, obs)
	}

	return out
}

func hasKey[V any](m map[string]V, key string) bool {
	_, ok := m[key]

	return ok
}

// shortName drops the package qualifier a go test id carries.
func shortName(id string) string {
	if i := strings.LastIndex(id, "."); i >= 0 {
		return id[i+1:]
	}

	return id
}

// targets is one `go test` invocation's arguments: which tests to run and
// which packages to run them from.
type targets struct {
	names    []string
	packages []string
}

func testTargets(tests []gate.TestFunc) targets {
	nameSet := map[string]struct{}{}
	pkgSet := map[string]struct{}{}

	for _, t := range tests {
		nameSet[t.Name] = struct{}{}
		pkgSet[t.Package] = struct{}{}
	}

	return targets{names: sortedKeys(nameSet), packages: sortedKeys(pkgSet)}
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	sort.Strings(out)

	return out
}

// PatternScanner runs one structural pattern over a tree. *astgrep.Runner
// satisfies it.
type PatternScanner interface {
	Pattern(ctx context.Context, repoRoot, pattern, language string, targets ...string) (astgrep.Report, error)
}

// AstGrepSweeper re-runs a recorded sweep through ast-grep, so the
// generalize phase's work list is the harness's rather than the model's
// recall.
type AstGrepSweeper struct {
	scanner PatternScanner
}

// NewAstGrepSweeper builds a Sweeper over scanner.
func NewAstGrepSweeper(scanner PatternScanner) *AstGrepSweeper {
	return &AstGrepSweeper{scanner: scanner}
}

// Sweep returns every site the recorded pattern matches today. An absent
// ast-grep is an error rather than an empty hit list, because a sweep that
// could not run must never read as a sweep with nothing left to triage.
func (s *AstGrepSweeper) Sweep(ctx context.Context, repoRoot string, sweep Sweep) ([]Hit, error) {
	language := sweep.Language
	if language == "" {
		language = DefaultSweepLanguage
	}

	var targets []string
	if sweep.Path != "" {
		targets = append(targets, sweep.Path)
	}

	report, err := s.scanner.Pattern(ctx, repoRoot, sweep.Pattern, language, targets...)
	if err != nil {
		return nil, fmt.Errorf("sweeping for %q: %w", sweep.Pattern, err)
	}

	out := make([]Hit, 0, len(report.Findings))
	for i := range report.Findings {
		out = append(out, Hit{File: report.Findings[i].File, Line: report.Findings[i].Start.Line})
	}

	return out, nil
}
