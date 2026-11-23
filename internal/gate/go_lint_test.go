package gate_test

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kyleking/wavez/internal/gate"
)

// fakeLinter writes a stand-in golangci-lint that prints output and exits
// with status, and returns the option that resolves the tool to it.
func fakeLinter(t *testing.T, root, output, status string) gate.Option {
	t.Helper()

	writeFile(t, root, ".fake/out.txt", output)
	writeFile(t, root, ".fake/golangci-lint", "#!/bin/sh\ncat \"$(dirname \"$0\")/out.txt\"\nexit "+status+"\n")

	return replacing("golangci-lint", filepath.Join(root, ".fake", "golangci-lint"))
}

func TestRun_LintKeepsFindingsInChangedFiles(t *testing.T) {
	t.Parallel()

	const (
		mine   = "a/a.go:3:1: exported function A should have comment (revive)\n\tfunc A() int { return 1 }\n\t^\n"
		theirs = "a/old.go:9:2: ineffectual assignment to x (ineffassign)\n\tx := 1\n\t^\n"
		tally  = "2 issues:\n* ineffassign: 1\n* revive: 1\n"
	)

	tests := []struct {
		name   string
		output string
		status string
		config string
		wants  []want
	}{
		{
			name: "a clean run passes", status: "0", config: ".golangci.toml",
			wants: []want{{"lint", gate.StatusPass, ""}},
		},
		{
			name: "a finding in a changed file fails, without the excerpt", output: mine + theirs + tally, status: "1",
			config: ".golangci.yml", wants: []want{{"lint", gate.StatusFail, "a/a.go:3:1: exported function A"}},
		},
		{
			name: "findings only in files the change did not touch pass", output: theirs + tally, status: "1",
			config: ".golangci.yaml", wants: []want{{"lint", gate.StatusPass, ""}},
		},
		{
			name:   "a package that does not compile is the build check's to report",
			output: "a/a.go:3:23: undefined: missing (typecheck)\n", status: "1", config: ".golangci.json",
			wants: []want{{"lint", gate.StatusAbstained, "the change does not compile"}},
		},
		{
			name: "a linter that cannot load abstains with its error",
			output: "level=warning msg=\"something deprecated\"\n" +
				"Error: can't load config: the Go language version (go1.24) used to build golangci-lint " +
				"is lower than the targeted Go version (1.26.0)\n",
			status: "3", config: ".golangci.toml",
			wants: []want{{"lint", gate.StatusAbstained, "used to build golangci-lint is lower"}},
		},
		{name: "a project without a linter config is not linted", output: mine, status: "1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			files := module(map[string]string{".wavez.toml": "disable = [\"build\", \"test\"]\n"})
			if tt.config != "" {
				files[tt.config] = ""
			}

			root := project(t, files)
			report := runGate(t.Context(), t, root, gate.StageStop, []string{"a/a.go"},
				fakeLinter(t, root, tt.output, tt.status))

			expect(t, report, tt.wants...)

			if len(tt.wants) > 0 && tt.wants[0].status == gate.StatusFail {
				const finding = "a/a.go:3:1: exported function A should have comment (revive)"
				if got := report.Results[0].Summary; got != finding {
					t.Errorf("summary kept more than the finding:\n%s", got)
				}
			}
		})
	}
}

func TestRun_LintAbstainsOrStaysOut(t *testing.T) {
	t.Parallel()

	t.Run("no linter on PATH abstains", func(t *testing.T) {
		t.Parallel()

		root := project(t, module(map[string]string{".golangci.toml": "version = \"2\"\n"}))
		report := runGate(t.Context(), t, root, gate.StageStop, []string{"a/a.go"}, without("golangci-lint"))

		expect(t, report, want{"build", gate.StatusPass, ""}, want{"test", gate.StatusPass, ""},
			want{"lint", gate.StatusAbstained, "golangci-lint not on PATH"})
	})

	t.Run("a failed build reports alone", func(t *testing.T) {
		t.Parallel()

		root := project(t, module(map[string]string{
			".golangci.toml": "version = \"2\"\n", "a/a.go": srcABroken,
		}))
		report := runGate(t.Context(), t, root, gate.StageStop, []string{"a/a.go"},
			fakeLinter(t, root, "a/a.go:3:23: undefined: missing (typecheck)\n", "1"))

		expect(t, report, want{"build", gate.StatusFail, "undefined: missing"})
	})

	t.Run("a disabled lint does not run", func(t *testing.T) {
		t.Parallel()

		root := project(t, module(map[string]string{
			".golangci.toml": "version = \"2\"\n", ".wavez.toml": "disable = [\"lint\", \"build\", \"test\"]\n",
		}))
		expect(t, runGate(t.Context(), t, root, gate.StageStop, []string{"a/a.go"}, without("golangci-lint")))
	})
}

// TestRun_LintWithTheRealLinter runs whatever golangci-lint the machine has.
// One that cannot lint this fixture (too old for the toolchain, or absent)
// abstains, which is the behavior under test everywhere else, so here it
// skips.
func TestRun_LintWithTheRealLinter(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("golangci-lint"); err != nil {
		t.Skip("NOT TESTED: golangci-lint is not on PATH, so the lint check never ran against the real linter")
	}

	root := project(t, module(map[string]string{
		".wavez.toml":    "disable = [\"build\", \"test\"]\n",
		".golangci.toml": "version = \"2\"\n\n[linters]\ndefault = \"none\"\nenable = [\"ineffassign\"]\n",
		"a/a.go":         "package a\n\n// A returns one.\nfunc A() int {\n\tx := 2\n\tx = 1\n\n\treturn 1\n}\n",
		"lone/l.go":      "package lone\n\n// L is unrelated.\nfunc L() {\n\ty := 2\n\ty = 1\n}\n",
	}))

	report := runGate(t.Context(), t, root, gate.StageStop, []string{"a/a.go"})
	if len(report.Results) == 1 && report.Results[0].Status == gate.StatusAbstained {
		t.Skipf("NOT TESTED: the golangci-lint on PATH cannot lint the fixture, so the real linter did not run: %s",
			report.Results[0].Summary)
	}

	expect(t, report, want{"lint", gate.StatusFail, "a/a.go:6:2: ineffectual assignment to x"})
}
