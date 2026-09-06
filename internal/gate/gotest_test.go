package gate_test

import (
	"bytes"
	"context"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/tool"
)

var update = flag.Bool("update", false, "regenerate golden go test -json fixtures")

func goldenPath(t *testing.T, name string) string {
	t.Helper()

	return filepath.Join("testdata", name+".golden")
}

func regenerateGolden(t *testing.T, name string) {
	t.Helper()

	dir := filepath.Join("testdata", "fixture", "gotestjson", name)
	cmd := exec.CommandContext(context.Background(), "go", "test", "-json", "./...")
	cmd.Dir = dir

	//nolint:errcheck // go test exits non-zero for the failing scenarios; the JSON stream is what matters
	out, _ := cmd.Output()

	if err := os.WriteFile(goldenPath(t, name), out, 0o600); err != nil {
		t.Fatalf("writing golden %s: %v", name, err)
	}
}

func loadGolden(t *testing.T, name string) []byte {
	t.Helper()

	if *update {
		regenerateGolden(t, name)
	}

	data, err := os.ReadFile(goldenPath(t, name))
	if err != nil {
		t.Fatalf("reading golden %s: %v", name, err)
	}

	return data
}

func TestParseGoTestJSON(t *testing.T) {
	t.Parallel()

	t.Run("pass", testParseGoTestJSONPass)
	t.Run("fail", testParseGoTestJSONFail)
	t.Run("panic", testParseGoTestJSONPanic)
	t.Run("buildfail", testParseGoTestJSONBuildFail)
}

func testParseGoTestJSONPass(t *testing.T) {
	t.Parallel()

	summary, err := gate.ParseGoTestJSON(bytes.NewReader(loadGolden(t, "pass")))
	if err != nil {
		t.Fatalf("ParseGoTestJSON: %v", err)
	}
	if !summary.Pass {
		t.Errorf("Pass = false, want true")
	}
	if summary.BuildFailed {
		t.Errorf("BuildFailed = true, want false")
	}
	if len(summary.FailedTests) != 0 {
		t.Errorf("FailedTests = %+v, want none", summary.FailedTests)
	}
	if len(summary.PassedTests) != 1 || summary.PassedTests[0] != "gtjpass.TestOK" {
		t.Errorf("PassedTests = %v, want [gtjpass.TestOK]", summary.PassedTests)
	}
}

func testParseGoTestJSONFail(t *testing.T) {
	t.Parallel()

	summary, err := gate.ParseGoTestJSON(bytes.NewReader(loadGolden(t, "fail")))
	if err != nil {
		t.Fatalf("ParseGoTestJSON: %v", err)
	}
	if summary.Pass {
		t.Errorf("Pass = true, want false")
	}
	if summary.BuildFailed {
		t.Errorf("BuildFailed = true, want false (a test failure, not a build failure)")
	}
	if len(summary.FailedTests) != 1 || summary.FailedTests[0].Name != "TestGreet" {
		t.Fatalf("FailedTests = %+v, want one TestGreet failure", summary.FailedTests)
	}
	if !containsSubstring(summary.FailedTests[0].Output, "greet_test.go:7") {
		t.Errorf("FailedTests[0].Output = %v, want a line for greet_test.go:7", summary.FailedTests[0].Output)
	}
}

func testParseGoTestJSONPanic(t *testing.T) {
	t.Parallel()

	summary, err := gate.ParseGoTestJSON(bytes.NewReader(loadGolden(t, "panic")))
	if err != nil {
		t.Fatalf("ParseGoTestJSON: %v", err)
	}
	if summary.Pass {
		t.Errorf("Pass = true, want false")
	}
	if len(summary.FailedTests) != 1 || summary.FailedTests[0].Name != "TestPanics" {
		t.Fatalf("FailedTests = %+v, want one TestPanics failure", summary.FailedTests)
	}
	if !containsSubstring(summary.FailedTests[0].Output, "panic: assignment to entry in nil map") {
		t.Errorf("FailedTests[0].Output missing the panic message: %v", summary.FailedTests[0].Output)
	}
}

func testParseGoTestJSONBuildFail(t *testing.T) {
	t.Parallel()

	summary, err := gate.ParseGoTestJSON(bytes.NewReader(loadGolden(t, "buildfail")))
	if err != nil {
		t.Fatalf("ParseGoTestJSON: %v", err)
	}
	if summary.Pass {
		t.Errorf("Pass = true, want false")
	}
	if !summary.BuildFailed {
		t.Errorf("BuildFailed = false, want true")
	}
	if len(summary.FailedTests) != 1 {
		t.Fatalf("FailedTests = %+v, want one build-failure entry", summary.FailedTests)
	}
	if !containsSubstring(summary.FailedTests[0].Output, "broken.go:4:9") {
		t.Errorf("FailedTests[0].Output missing the compile error: %v", summary.FailedTests[0].Output)
	}
}

func TestTrimFailure(t *testing.T) {
	t.Parallel()

	failure := gate.FailedTest{
		Name:    "TestGreet",
		Package: "gtjfail",
		Output: []string{
			"=== RUN   TestGreet\n",
			"    greet_test.go:7: Greet(\"Ada\") = \"hi Ada\", want \"hello Ada\"\n",
			"--- FAIL: TestGreet (0.00s)\n",
			"    unrelated_helper.go:99: some helper detail that never touched the change\n",
		},
	}

	got := gate.TrimFailure(failure, []string{"internal/gate/testdata/fixture/gotestjson/fail/greet_test.go"})

	if len(got.Frames) != 1 {
		t.Fatalf("Frames = %v, want exactly the one line referencing greet_test.go", got.Frames)
	}
	if got.Test != "TestGreet" || got.Package != "gtjfail" {
		t.Errorf("Test/Package = %q/%q, want TestGreet/gtjfail", got.Test, got.Package)
	}
}

// ruff, ty, and rustc put the rule and the message on one line and the
// location on the next, so trimming to lines naming a changed file used to
// hand a run a column and no complaint.
func TestTrimFailure_KeepsTheMessageAboveALocationOnlyFrame(t *testing.T) {
	t.Parallel()

	failure := gate.FailedTest{
		Name: "lint",
		Output: []string{
			"error[unused-import] `os` imported but unused",
			"  --> db_slice/schema.py:148:12",
			"   |",
			"note: this is unrelated",
			"  --> other/untouched.py:3:1",
		},
	}

	got := gate.TrimFailure(failure, []string{"db_slice/schema.py"})
	want := []string{
		"error[unused-import] `os` imported but unused",
		"  --> db_slice/schema.py:148:12",
	}

	if len(got.Frames) != len(want) {
		t.Fatalf("Frames = %q, want %q", got.Frames, want)
	}

	for i := range want {
		if got.Frames[i] != want[i] {
			t.Fatalf("Frames = %q, want %q", got.Frames, want)
		}
	}
}

func TestTrimFailureDropsFramesThatDoNotTouchChangedFiles(t *testing.T) {
	t.Parallel()

	failure := gate.FailedTest{
		Name: "TestPanics",
		Output: []string{
			"panic: boom\n",
			"testing.tRunner.func1()\n",
			"\t/usr/local/go/src/testing/testing.go:1977 +0x318\n",
		},
	}

	got := gate.TrimFailure(failure, []string{"panic/panic_test.go"})

	if len(got.Frames) != 0 {
		t.Errorf("Frames = %v, want none: nothing in the output references panic_test.go", got.Frames)
	}

	if !containsSubstring(got.Context, "panic: boom") {
		t.Errorf("Context = %v, want the head of the untrimmed output", got.Context)
	}
}

// A build failure the harness caused prints a toolchain error and no source
// line, so trimming keeps nothing. Measured on `h5`: told only the gate
// name, the run spent fourteen turns hunting a defect that did not exist.
func TestTrimFailureCarriesAToolchainErrorNoFrameCanName(t *testing.T) {
	t.Parallel()

	got := gate.TrimFailure(gate.FailedTest{
		Package: "internal/guard",
		Output: []string{
			"# internal/guard\n",
			"package internal/guard is not in std (/usr/local/go/src/internal/guard)\n",
			"FAIL\tinternal/guard [setup failed]\n",
		},
	}, []string{"internal/guard/sequence.go"})

	if len(got.Frames) != 0 {
		t.Fatalf("Frames = %v, want none", got.Frames)
	}

	if !containsSubstring(got.Context, "is not in std") {
		t.Errorf("Context = %v, want the line saying the command itself was wrong", got.Context)
	}
}

// The fallback context is bounded, so a line spent on `go test`'s own
// announcements or on a message already shown is a line the fault does not
// get. Both shapes are taken from the gate deliveries recorded in this
// project's thread logs, where scaffolding was 55.5% of the lines under a
// verbose test failure and a repeated message 45.4% of the lines under a
// build failure.
func TestTrimFailure_ContextDropsScaffoldingAndCollapsesARepeatedMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		output []string
		want   []string
		absent []string
	}{
		{
			name: "a verbose test run",
			output: []string{
				"=== RUN   TestManagerRestore\n",
				"=== PAUSE TestManagerRestore\n",
				"=== CONT  TestManagerRestore\n",
				"    restore_test.go:222: restore error = no repository, want no such checkpoint\n",
				"--- FAIL: TestManagerRestore (0.00s)\n",
			},
			want:   []string{"restore error =", "--- FAIL: TestManagerRestore"},
			absent: []string{"=== RUN", "=== PAUSE", "=== CONT"},
		},
		{
			name: "a build failure repeating one message",
			output: []string{
				"# github.com/kyleking/wavez/internal/bench_test\n",
				"internal/bench/stats_test.go:42:23: undefined: bench.Read\n",
				"internal/bench/stats_test.go:93:23: undefined: bench.Read\n",
				"internal/bench/stats_test.go:264:23: undefined: bench.Read\n",
				"internal/bench/stats_test.go:310:23: undefined: bench.Read\n",
				"internal/bench/stats_test.go:portable:1: second distinct complaint\n",
				"FAIL\tgithub.com/kyleking/wavez/internal/bench [build failed]\n",
			},
			want: []string{
				"stats_test.go:42:23: undefined: bench.Read",
				"also at internal/bench/stats_test.go:93:23",
				"second distinct complaint",
				"[build failed]",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := gate.TrimFailure(gate.FailedTest{Name: "x", Output: tt.output}, []string{"unrelated.go"})

			if len(got.Frames) != 0 {
				t.Fatalf("Frames = %v, want none: nothing here names a changed file", got.Frames)
			}

			for _, want := range tt.want {
				if !containsSubstring(got.Context, want) {
					t.Errorf("Context = %v, want a line carrying %q", got.Context, want)
				}
			}

			for _, absent := range tt.absent {
				if containsSubstring(got.Context, absent) {
					t.Errorf("Context = %v, want %q dropped", got.Context, absent)
				}
			}
		})
	}
}

func containsSubstring(lines []string, substr string) bool {
	for _, l := range lines {
		if strings.Contains(l, substr) {
			return true
		}
	}

	return false
}

func TestGoTestGateEndToEnd(t *testing.T) {
	t.Parallel()

	repoRoot := copyFixtureModule(t, "gotestjson/fail")

	g := gate.NewGoTestGate(repoRoot)
	rc := gate.RunContext{
		RepoRoot: repoRoot,
		Changes:  []tool.Change{{Path: "greet_test.go"}},
		Selection: gate.Selection{
			Level:    gate.LevelPackage,
			Packages: []string{"."},
		},
	}

	result, err := g.Run(context.Background(), rc)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Pass {
		t.Fatalf("Pass = true, want false: the fixture module's TestGreet always fails")
	}
	if len(result.Failures) != 1 || result.Failures[0].Test != "TestGreet" {
		t.Fatalf("Failures = %+v, want one TestGreet failure", result.Failures)
	}
}

// A change set with no Go file holds no work for the test gate: running the
// selection's directory guess as a package reported "no Go files" as a
// failure with nothing after it, on a README edit.
func TestGoTestGateAbstainsWhenNoGoFileChanged(t *testing.T) {
	t.Parallel()

	repoRoot := copyFixtureModule(t, "gotestjson/fail")

	g := gate.NewGoTestGate(repoRoot)
	rc := gate.RunContext{
		RepoRoot:  repoRoot,
		Changes:   []tool.Change{{Path: "README.md"}},
		Selection: gate.Selection{Level: gate.LevelPackage, Packages: []string{"."}},
	}

	result, err := g.Run(context.Background(), rc)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Pass || result.Examined != 0 || result.Reason == "" {
		t.Fatalf("result = %+v, want a reasoned abstention", result)
	}
}
