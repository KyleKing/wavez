package reduce_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/reduce"
)

func TestOutputKeepsWhatNamesTheFailure(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("testdata/gotest_verbose.txt")
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}

	got := reduce.Output(string(raw))

	for _, want := range []string{
		"want 42, got 7",
		"--- FAIL: TestMany/o1",
		"FAIL\ttrimdemo",
		"(59 subtests passed)",
	} {
		if !strings.Contains(got.Text, want) {
			t.Errorf("reduced output dropped %q:\n%s", want, got.Text)
		}
	}

	if strings.Contains(got.Text, "=== RUN") || strings.Contains(got.Text, "--- PASS") {
		t.Errorf("reduced output kept per-test noise:\n%s", got.Text)
	}

	if len(got.Text) > len(raw)/10 {
		t.Errorf("reduced %d bytes to %d, want under a tenth", len(raw), len(got.Text))
	}

	if again := reduce.Output(got.Text); again.Text != got.Text {
		t.Errorf("reducing twice changed the text:\n%s\n\n%s", got.Text, again.Text)
	}
}

func TestOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		in    string
		holds []string
		gone  []string
	}{
		{
			name:  "a build failure keeps each diagnostic once",
			in:    "# example/pkg\nmain.go:7:2: undefined: x\nmain.go:7:2: undefined: x\nmain.go:9:2: undefined: y\n",
			holds: []string{"main.go:7:2: undefined: x", "main.go:9:2: undefined: y", "2 of 4 lines dropped"},
			gone:  []string{"# example/pkg"},
		},
		{
			name: "a build failure inside go test is read as a build failure",
			in: "# example/pkg [example/pkg.test]\nmain_test.go:3:1: syntax error\n" +
				"FAIL\texample/pkg [build failed]\n",
			holds: []string{"main_test.go:3:1: syntax error", "build failed"},
			gone:  []string{"# example/pkg ["},
		},
		{
			name:  "a repeated warning is counted rather than repeated",
			in:    strings.Repeat("warning: deprecated call\n", 8) + "done\n",
			holds: []string{"warning: deprecated call", "repeats 8 times", "done"},
		},
		{
			name: "output with nothing to drop is returned whole",
			in:   "ok  \texample/pkg\t0.2s\n",
			gone: []string{"lines dropped"},
		},
		{
			name: "a passing verbose run collapses to its verdict",
			in: "=== RUN   TestA\n--- PASS: TestA (0.00s)\n=== RUN   TestB\n" +
				"--- PASS: TestB (0.00s)\nPASS\nok  \texample/pkg\t0.2s\n",
			holds: []string{"ok  \texample/pkg", "(2 subtests passed)"},
			gone:  []string{"=== RUN", "--- PASS"},
		},
		{
			name: "what a passing test printed survives",
			in: "=== RUN   TestA\n    a_test.go:9: measured 41ms\n--- PASS: TestA (0.00s)\n" +
				"PASS\nok  \texample/pkg\t0.2s\n",
			holds: []string{"a_test.go:9: measured 41ms", "ok  \texample/pkg"},
			gone:  []string{"=== RUN", "--- PASS"},
		},
		{
			name: "a sweep collapses its quiet packages and keeps the failures",
			in: "ok  \ta\t0.1s\nok  \tb\t0.1s\n?   \tc\t[no test files]\nok  \td\t0.1s\n" +
				"FAIL\te [build failed]\nFAIL\n",
			holds: []string{"FAIL\te [build failed]", "(4 packages ok or without tests)"},
			gone:  []string{"ok  \ta"},
		},
		{
			name: "empty output stays empty",
			in:   "",
			gone: []string{"dropped"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := reduce.Output(tt.in).Text

			for _, want := range tt.holds {
				if !strings.Contains(got, want) {
					t.Errorf("dropped %q:\n%s", want, got)
				}
			}

			for _, unwanted := range tt.gone {
				if strings.Contains(got, unwanted) {
					t.Errorf("kept %q:\n%s", unwanted, got)
				}
			}

			if again := reduce.Output(got).Text; again != got {
				t.Errorf("reducing twice changed the text:\n%s\n\n%s", got, again)
			}
		})
	}
}

// ruffFindings is the shape a `ruff check` run emits: a location, a rule code,
// and a message that names the thing it fired on.
func ruffFindings() string {
	var b strings.Builder

	for i := range 8 {
		fmt.Fprintf(&b, "src/vcr_tui/preview.py:%d:5: D102 Missing docstring in public method\n", 10+i)
	}

	for i := range 4 {
		fmt.Fprintf(&b, "tests/test_ui.py:%d:1: D103 Missing docstring in public function\n", 20+i)
	}

	fmt.Fprint(&b, "src/vcr_tui/app.py:7:1: ANN201 Missing return type annotation for `run`\n")
	fmt.Fprint(&b, "src/vcr_tui/cli.py:9:1: ANN201 Missing return type annotation for `main`\n")
	fmt.Fprint(&b, "Found 14 errors.\n")

	return b.String()
}

func TestOutputGroupsManyFindingsByKind(t *testing.T) {
	t.Parallel()

	got := reduce.Output(ruffFindings()).Text

	for _, want := range []string{
		"14 findings across 4 files in 3 kinds",
		"   8x D102 Missing docstring in public method",
		"   4x D103 Missing docstring in public function",
		"   2x ANN201 Missing return type annotation for `run`",
		"src/vcr_tui/preview.py:10:5, src/vcr_tui/preview.py:11:5, src/vcr_tui/preview.py:12:5 and 5 more",
		"Found 14 errors.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}

	if second := reduce.Output(got).Text; second != got {
		t.Errorf("second pass changed the text:\n%s", second)
	}
}

// Grouping that does not halve the output costs a reader the sites and buys
// nothing, so a handful of distinct errors stays one per line.
func TestOutputLeavesDistinctDiagnosticsAlone(t *testing.T) {
	t.Parallel()

	raw := "# github.com/kyleking/wavez/internal/tool\n" +
		"internal/tool/a.go:12:2: declared and not used: name\n" +
		"internal/tool/b.go:44:9: undefined: Helper\n"

	got := reduce.Output(raw).Text
	if strings.Contains(got, "kinds") {
		t.Errorf("grouped output it should have left alone:\n%s", got)
	}

	if !strings.Contains(got, "undefined: Helper") {
		t.Errorf("dropped a diagnostic:\n%s", got)
	}
}
