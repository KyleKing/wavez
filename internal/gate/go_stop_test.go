package gate_test

import (
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/gate"
)

// The sources of the fixture module, and the variants a case swaps in.
const (
	srcA = "package a\n\n// A returns one.\nfunc A() int { return 1 }\n"
	// A value of three compiles and fails TestA and TestB.
	srcAThree = "package a\n\nfunc A() int { return 3 }\n"
	// A value of nine compiles and fails TestC; cases using it drop TestA.
	srcANine   = "package a\n\nfunc A() int { return 9 }\n"
	srcABroken = "package a\n\nfunc A() int { return missing }\n"
	srcAString = "package a\n\nfunc A() string { return \"1\" }\n"
	srcACycle  = "package a\n\nimport _ \"example.com/scratch/b\"\n\nfunc A() int { return 1 }\n"
	srcATest   = "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {\n" +
		"\tif A() != 1 {\n\t\tt.Fatal(\"A() is not 1\")\n\t}\n}\n"
	srcATestBad = "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) { nope() }\n"
	srcATestRed = "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) { t.Fatal(\"boom\") }\n"
	srcB        = "package b\n\nimport \"example.com/scratch/a\"\n\n" +
		"// B doubles A.\nfunc B() int { return 2 * a.A() }\n"
	srcBHelper = "package b\n\n// B calls a helper that was deleted.\nfunc B() int { return helper() }\n"
	srcBTest   = "package b\n\nimport \"testing\"\n\nfunc TestB(t *testing.T) {\n" +
		"\tif B() != 2 {\n\t\tt.Fatalf(\"B() = %d, want 2\", B())\n\t}\n}\n"
	srcCTest = "package c_test\n\nimport (\n\t\"testing\"\n\n\t\"example.com/scratch/a\"\n)\n\n" +
		"func TestC(t *testing.T) {\n\tif a.A() > 5 {\n\t\tt.Fatal(\"A() is over 5\")\n\t}\n}\n"
	srcLone       = "package lone\n\n// L is unrelated to the rest.\nfunc L() {}\n"
	srcLoneBroken = "package lone\n\nfunc L() { missing() }\n"
	removed       = ""
)

// module is a four-package fixture: a is a leaf, b imports a, c holds only
// an external test that imports a, and lone touches none of them. An
// override replaces a file, or removes it when it is the removed marker.
func module(overrides map[string]string) map[string]string {
	files := map[string]string{
		"go.mod": goMod, "a/a.go": srcA, "a/a_test.go": srcATest, "b/b.go": srcB, "b/b_test.go": srcBTest,
		"c/c_test.go": srcCTest, "lone/l.go": srcLone,
	}

	for name, body := range overrides {
		if body == removed {
			delete(files, name)

			continue
		}

		files[name] = body
	}

	return files
}

type stopCase struct {
	name      string
	overrides map[string]string
	changed   []string
	opts      []gate.Option
	wants     []want
}

func runStopCases(t *testing.T, tests []stopCase) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := project(t, module(tt.overrides))
			report := runGate(t.Context(), t, root, gate.StageStop, tt.changed, tt.opts...)

			expect(t, report, tt.wants...)
		})
	}
}

func passed(checks ...string) []want {
	out := make([]want, len(checks))
	for i, c := range checks {
		out[i] = want{c, gate.StatusPass, ""}
	}

	return out
}

func TestRun_StopStageSelectsAffectedPackages(t *testing.T) {
	t.Parallel()

	runStopCases(t, []stopCase{
		{name: "a clean change passes both", changed: []string{"a/a.go"}, wants: passed("build", "test")},
		{
			name: "a compile error fails build and test is not reported", changed: []string{"a/a.go"},
			overrides: map[string]string{"a/a.go": srcABroken},
			wants:     []want{{"build", gate.StatusFail, "a/a.go:3:23: undefined: missing"}},
		},
		{
			name: "a change that breaks an importer fails build there", changed: []string{"a/a.go"},
			overrides: map[string]string{"a/a.go": srcAString, "a/a_test.go": removed},
			wants:     []want{{"build", gate.StatusFail, "b/b.go:6:"}},
		},
		{
			name: "a test that does not compile is a build failure", changed: []string{"a/a_test.go"},
			overrides: map[string]string{"a/a_test.go": srcATestBad},
			wants:     []want{{"build", gate.StatusFail, "a/a_test.go:5:28: undefined: nope"}},
		},
		{
			name: "a failing test in the changed package fails test", changed: []string{"a/a.go"},
			overrides: map[string]string{"a/a.go": srcAThree},
			wants:     []want{{"build", gate.StatusPass, ""}, {"test", gate.StatusFail, "A() is not 1"}},
		},
		{
			name: "a failing test in an importer fails test", changed: []string{"a/a.go"},
			overrides: map[string]string{"a/a.go": srcANine, "a/a_test.go": removed},
			wants:     []want{{"build", gate.StatusPass, ""}, {"test", gate.StatusFail, "A() is over 5"}},
		},
		{
			name: "an unrelated package is not tested", changed: []string{"lone/l.go"},
			overrides: map[string]string{"a/a_test.go": srcATestRed}, wants: passed("build", "test"),
		},
		{
			name: "a deleted file still selects its package", changed: []string{"b/helper.go"},
			overrides: map[string]string{"b/b.go": srcBHelper},
			wants:     []want{{"build", gate.StatusFail, "undefined: helper"}},
		},
		{
			name: "a deleted package still selects its importers", changed: []string{"a/a.go"},
			overrides: map[string]string{"a/a.go": removed, "a/a_test.go": removed},
			wants:     []want{{"build", gate.StatusFail, "b/b.go:3:8:"}},
		},
		{
			name: "a package holding only tests is tested and not built", changed: []string{"c/c_test.go"},
			wants: passed("build", "test"),
		},
		{
			name: "a go.mod change checks the whole module", changed: []string{"go.mod"},
			overrides: map[string]string{"lone/l.go": srcLoneBroken},
			wants:     []want{{"build", gate.StatusFail, "lone/l.go:3:12: undefined: missing"}},
		},
		{
			name: "an import cycle is the change's failure", changed: []string{"a/a.go"},
			overrides: map[string]string{"a/a.go": srcACycle},
			wants:     []want{{"build", gate.StatusFail, "import cycle not allowed"}},
		},
		{name: "a change with no Go in it selects nothing", changed: []string{"README.md", "testdata/x.go"}},
	})
}

func TestRun_StopStageAbstainsAndHonorsDisable(t *testing.T) {
	t.Parallel()

	const (
		noTest  = "disable = [\"test\"]\n"
		noBuild = "disable = [\"build\"]\n"
	)

	runStopCases(t, []stopCase{
		{
			name: "a go.mod go cannot read abstains instead of failing", changed: []string{"a/a.go"},
			overrides: map[string]string{"go.mod": "module example.com/scratch\n\nnonsense directive\n"},
			wants:     []want{{"build", gate.StatusAbstained, "go.mod"}, {"test", gate.StatusAbstained, "go.mod"}},
		},
		{
			name: "no go binary abstains", changed: []string{"a/a.go"}, opts: []gate.Option{without("go")},
			wants: []want{
				{"build", gate.StatusAbstained, "go not on PATH"},
				{"test", gate.StatusAbstained, "go not on PATH"},
			},
		},
		{
			name: "with test disabled, test code is still compiled", changed: []string{"a/a_test.go"},
			overrides: map[string]string{".wavez.toml": noTest, "a/a_test.go": srcATestBad},
			wants:     []want{{"build", gate.StatusFail, "undefined: nope"}},
		},
		{
			name: "with test disabled, a failing test is not run", changed: []string{"a/a.go"},
			overrides: map[string]string{".wavez.toml": noTest, "a/a.go": srcAThree}, wants: passed("build"),
		},
		{
			name: "with build disabled, test reports the compile error", changed: []string{"a/a.go"},
			overrides: map[string]string{".wavez.toml": noBuild, "a/a.go": srcABroken},
			wants:     []want{{"test", gate.StatusFail, "undefined: missing"}},
		},
		{
			name: "with both disabled nothing runs", changed: []string{"a/a.go"},
			overrides: map[string]string{".wavez.toml": "disable = [\"build\", \"test\"]\n"},
		},
	})
}

func TestRun_TestFailureSummaryIsReduced(t *testing.T) {
	t.Parallel()

	root := project(t, module(map[string]string{"a/a.go": srcAThree}))
	report := runGate(t.Context(), t, root, gate.StageStop, []string{"a/a.go"})

	expect(t, report, want{"build", gate.StatusPass, ""}, want{"test", gate.StatusFail, "--- FAIL: TestA"})

	summary := report.Results[1].Summary
	if !strings.Contains(summary, "--- FAIL: TestB") || !strings.Contains(summary, "B() = 6, want 2") {
		t.Errorf("summary lost the importer's failure:\n%s", summary)
	}

	if strings.Contains(summary, "=== RUN") {
		t.Errorf("summary kept runner noise:\n%s", summary)
	}

	if !strings.HasPrefix(report.Render(), "FAIL test\n") {
		t.Errorf("Render() =\n%s", report.Render())
	}
}
