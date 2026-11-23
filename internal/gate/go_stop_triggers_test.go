package gate_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/gate"
)

const (
	srcHangTest = "package a\n\nimport \"testing\"\n\nfunc TestHang(t *testing.T) { select {} }\n"
	srcMain     = "package main\n\nfunc main() {}\n"
	// The data test fails unless testdata/want.txt holds "1".
	srcDataTest = "package a\n\nimport (\n\t\"os\"\n\t\"strings\"\n\t\"testing\"\n)\n\n" +
		"func TestData(t *testing.T) {\n\twant, err := os.ReadFile(\"testdata/want.txt\")\n" +
		"\tif err != nil || strings.TrimSpace(string(want)) != \"1\" {\n" +
		"\t\tt.Fatalf(\"want.txt holds %q\", want)\n\t}\n}\n"
	// The embedding source and its test fail unless tmpl.txt holds "1".
	srcEmbed = "package a\n\nimport _ \"embed\"\n\n//go:embed tmpl.txt\nvar tmpl string\n\n" +
		"// A returns one.\nfunc A() int { return 1 }\n"
	srcEmbedTest = "package a\n\nimport (\n\t\"strings\"\n\t\"testing\"\n)\n\n" +
		"func TestTmpl(t *testing.T) {\n\tif strings.TrimSpace(tmpl) != \"1\" {\n" +
		"\t\tt.Fatalf(\"tmpl holds %q\", tmpl)\n\t}\n}\n"
)

// hangBudget makes `go test -timeout` six seconds, which a hanging test
// runs out. The `go test` process gets three seconds more than that, and
// they have to cover compiling and linking the fixture, which is why warm
// compiles it first: on a cold cache a slow machine spends the whole gap in
// the compiler, and the hang is then cut off before it can be named.
const hangBudget = 15 * time.Second

// warm compiles the fixture's tests without running any.
func warm(t *testing.T, root string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "go", "test", "-run", "^$", "./...")
	cmd.Dir = root

	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
}

func TestRun_HangingTestFailsWithItsName(t *testing.T) {
	t.Parallel()

	t.Run("a hang the session wrote is a failure that names the test", func(t *testing.T) {
		t.Parallel()

		root := project(t, module(map[string]string{"a/hang_test.go": srcHangTest}))
		warm(t, root)

		started := time.Now()
		report := runGate(t.Context(), t, root, gate.StageStop, []string{"a/hang_test.go"}, gate.WithBudget(hangBudget))

		got := result(t, report, "test")
		assert.Equal(t, gate.StatusFail, got.Status, "%+v", got)
		assert.Contains(t, got.Summary, "panic: test timed out after 6s")
		assert.Contains(t, got.Summary, "TestHang (6s)")
		assert.Contains(t, got.Summary, "(a/hang_test.go:5)", "the frame is relative to the project")
		assert.NotContains(t, got.Summary, root)
		assert.NotContains(t, got.Summary, "goroutine")
		assert.Less(t, time.Since(started), hangBudget, "the run ends inside its budget")
	})

	t.Run("a hang the baseline already had goes to the person", func(t *testing.T) {
		t.Parallel()

		root := committed(t, module(map[string]string{"a/hang_test.go": srcHangTest}))
		ctx := began(t, root)
		writeFile(t, root, "a/extra.go", srcExtra)
		warm(t, root)

		// The baseline hangs for its own twelve seconds after the session's,
		// which leaves six of the thirty for everything else.
		report := runGate(ctx, t, root, gate.StageStop, []string{"a/extra.go"}, gate.WithBudget(2*hangBudget))

		got := result(t, report, "test")
		assert.Equal(t, gate.StatusPreexisting, got.Status, "%+v", got)
		assert.Equal(t, []string{"example.com/scratch/a TestHang"}, got.Preexisting)
		assert.False(t, report.Failed())
		assert.Contains(t, report.Notes(), "ALREADY FAILING")
	})
}

func TestRun_AbstainsWhenTheBudgetRunsOut(t *testing.T) {
	t.Parallel()

	root := project(t, map[string]string{
		".wavez.toml": check("slow", `command = "sleep 20"`),
		"a.txt":       "a\n",
	})

	started := time.Now()
	report := runGate(t.Context(), t, root, gate.StageStop, []string{"a.txt"}, gate.WithBudget(time.Second))

	expect(t, report, want{"slow", gate.StatusAbstained, "time budget ran out"})
	assert.True(t, report.Abstained())
	assert.False(t, report.Failed(), "a check that could not finish is not a failure")
	assert.Less(t, time.Since(started), 10*time.Second)
}

func TestRun_NonGoFileSelectsItsPackage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		overrides map[string]string
		changed   []string
		wants     []want
	}{
		{
			name: "test data a test reads",
			overrides: map[string]string{
				"a/data_test.go": srcDataTest, "a/testdata/want.txt": "2\n",
			},
			changed: []string{"a/testdata/want.txt"},
			wants:   []want{{"build", gate.StatusPass, ""}, {"test", gate.StatusFail, "--- FAIL: TestData"}},
		},
		{
			name: "test data in a subdirectory of testdata",
			overrides: map[string]string{
				"a/data_test.go": srcDataTest, "a/testdata/want.txt": "2\n", "a/testdata/deep/x.json": "{}\n",
			},
			changed: []string{"a/testdata/deep/x.json"},
			wants:   []want{{"build", gate.StatusPass, ""}, {"test", gate.StatusFail, "--- FAIL: TestData"}},
		},
		{
			name: "a file the package embeds",
			overrides: map[string]string{
				"a/a.go": srcEmbed, "a/embed_test.go": srcEmbedTest, "a/tmpl.txt": "2\n",
			},
			changed: []string{"a/tmpl.txt"},
			wants:   []want{{"build", gate.StatusPass, ""}, {"test", gate.StatusFail, "--- FAIL: TestTmpl"}},
		},
		{
			name:      "a file in a directory with no Go above it selects nothing",
			overrides: map[string]string{"docs/guide.md": "# Guide\n"},
			changed:   []string{"docs/guide.md"},
		},
		{
			name:      "a vendored file selects nothing",
			overrides: map[string]string{"a/vendor/x/data.txt": "x\n"},
			changed:   []string{"a/vendor/x/data.txt"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := project(t, module(tt.overrides))
			expect(t, runGate(t.Context(), t, root, gate.StageStop, tt.changed), tt.wants...)
		})
	}
}

func TestDirsSelected(t *testing.T) {
	t.Parallel()

	root := project(t, map[string]string{
		"go.mod": goMod, "main.go": srcMain, "a/a.go": srcA, "a/testdata/src/fix/fix.go": "package fix\n",
		"a/assets/img/logo.svg": "<svg/>\n", "nested/go.mod": "module example.com/nested\n",
		"nested/n.go":     "package n\n",
		"nested/data.txt": "x\n", "docs/guide.md": "x\n", ".github/ci.yml": "x\n", "node_modules/p/i.go": "package p\n",
	})

	tests := []struct {
		name    string
		changed []string
		want    []string
	}{
		{name: "a Go file is in its own directory", changed: []string{"a/a.go"}, want: []string{"a"}},
		{name: "a deleted Go file still names its directory", changed: []string{"gone/g.go"}, want: []string{"gone"}},
		{name: "test data belongs to the package above it", changed: []string{"a/testdata/x.txt"}, want: []string{"a"}},
		{
			name:    "a Go fixture under testdata is not a package of its own",
			changed: []string{"a/testdata/src/fix/fix.go"}, want: []string{"a"},
		},
		{
			name:    "an asset climbs to the nearest package",
			changed: []string{"a/assets/img/logo.svg"}, want: []string{"a"},
		},
		{name: "a file beside the root package selects it", changed: []string{"docs/guide.md"}, want: []string{"."}},
		{name: "a nested module stops the walk", changed: []string{"nested/data.txt"}},
		{name: "a hidden directory selects nothing", changed: []string{".github/ci.yml"}},
		{name: "node_modules selects nothing", changed: []string{"node_modules/p/data.json"}},
		{name: "go.mod is the whole module's, not a directory's", changed: []string{"go.mod", "go.sum"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, gate.DirsSelected(root, tt.changed))
		})
	}
}

func TestRun_BuildLeavesNoBinaryInTheRoot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		files   map[string]string
		changed []string
	}{
		{
			name:    "one main package, which go build would link into the working directory",
			files:   map[string]string{"go.mod": goMod, "cmd/app/main.go": srcMain},
			changed: []string{"cmd/app/main.go"},
		},
		{
			name:    "a main package at the root",
			files:   map[string]string{"go.mod": goMod, "main.go": srcMain},
			changed: []string{"main.go"},
		},
		{
			name:    "many packages",
			files:   map[string]string{"go.mod": goMod, "cmd/app/main.go": srcMain, "a/a.go": srcA},
			changed: []string{"cmd/app/main.go", "a/a.go"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := project(t, tt.files)
			before := listing(t, root)

			report := runGate(t.Context(), t, root, gate.StageStop, tt.changed)

			assert.Equal(t, gate.StatusPass, result(t, report, "build").Status)
			assert.Equal(t, before, listing(t, root), "the build wrote into the project")
		})
	}
}

func TestRun_BuildFailureStillNamesTheLine(t *testing.T) {
	t.Parallel()

	root := project(t, map[string]string{
		"go.mod": goMod, "cmd/app/main.go": "package main\n\nfunc main() { missing() }\n",
	})
	report := runGate(t.Context(), t, root, gate.StageStop, []string{"cmd/app/main.go"})

	expect(t, report, want{"build", gate.StatusFail, "cmd/app/main.go:3:15: undefined: missing"})
}

// listing names every file under root outside wavez's own directory.
func listing(t *testing.T, root string) []string {
	t.Helper()

	var names []string

	require.NoError(t, filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, relErr := filepath.Rel(root, path)
		require.NoError(t, relErr)

		if entry.IsDir() && rel == ".wavez" {
			return filepath.SkipDir
		}

		if !entry.IsDir() {
			names = append(names, filepath.ToSlash(rel))
		}

		return nil
	}))

	return names
}

func TestRun_StopFormatsWhatAShellCommandWrote(t *testing.T) {
	t.Parallel()

	// No edit-stage run happens here: that is what a file written with
	// `cat > a/new.go <<EOF` looks like to the gates, since no edit tool
	// fired for it.
	root := project(t, module(map[string]string{"a/new.go": "package a\n\nfunc  New( ) int {return 1}\n"}))

	report := runGate(t.Context(), t, root, gate.StageStop, []string{"a/new.go"})

	assert.Equal(t, "package a\n\nfunc New() int { return 1 }\n", readFile(t, root, "a/new.go"))
	assert.Equal(t, []string{"a/new.go"}, result(t, report, "format").Fixed)
	assert.Contains(t, report.Render(), "REWRITTEN by a formatter, re-read before editing: a/new.go")
	assert.False(t, report.Failed())

	again := runGate(t.Context(), t, root, gate.StageStop, []string{"a/new.go"})
	assert.Empty(t, again.Render(), "a formatted change set has nothing to say")

	for _, res := range again.Results {
		assert.NotEqual(t, "format", res.Check)
	}
}

func TestRun_LintGetsACacheOfTheProjectsOwn(t *testing.T) {
	t.Parallel()

	root := project(t, module(map[string]string{".golangci.toml": "version = \"2\"\n"}))
	writeFile(t, root, ".fake/golangci-lint",
		"#!/bin/sh\necho \"a/a.go:1:1: cache is $GOLANGCI_LINT_CACHE (fake)\"\nexit 1\n")

	report := runGate(t.Context(), t, root, gate.StageStop, []string{"a/a.go"},
		replacing("golangci-lint", filepath.Join(root, ".fake", "golangci-lint")))

	resolved, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)

	got := result(t, report, "lint")
	assert.Equal(t, gate.StatusFail, got.Status)
	// The summary is relative to the root, so the cache reads as a path in it.
	assert.Contains(t, got.Summary, "cache is .wavez/cache/golangci-lint (fake)")
	assert.DirExists(t, filepath.Join(resolved, ".wavez", "cache", "golangci-lint"))
	assert.FileExists(t, filepath.Join(resolved, ".wavez", "cache", ".gitignore"))
}

func TestRun_LintKeepsACacheThePersonChose(t *testing.T) {
	t.Setenv("GOLANGCI_LINT_CACHE", "/somewhere/else")

	root := project(t, module(map[string]string{".golangci.toml": "version = \"2\"\n"}))
	writeFile(t, root, ".fake/golangci-lint",
		"#!/bin/sh\necho \"a/a.go:1:1: cache is $GOLANGCI_LINT_CACHE (fake)\"\nexit 1\n")

	report := runGate(t.Context(), t, root, gate.StageStop, []string{"a/a.go"},
		replacing("golangci-lint", filepath.Join(root, ".fake", "golangci-lint")))

	assert.Contains(t, result(t, report, "lint").Summary, "cache is /somewhere/else (fake)")
}

func TestChecks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		files    map[string]string
		builtins []string
		custom   []string
	}{
		{
			name:     "a Go module with no linter configured",
			files:    map[string]string{"go.mod": goMod},
			builtins: []string{"format", "build", "test"},
		},
		{
			name:     "a Go module with a linter",
			files:    map[string]string{"go.mod": goMod, ".golangci.toml": "version = \"2\"\n"},
			builtins: []string{"format", "build", "test", "lint"},
		},
		{
			name:     "a built-in turned off",
			files:    map[string]string{"go.mod": goMod, ".wavez.toml": "disable = [\"test\"]\n"},
			builtins: []string{"format", "build"},
		},
		{
			name: "another language with its own stop check",
			files: map[string]string{".wavez.toml": check("ruff", `command = "ruff check {files}"`) +
				check("fmt", "command = \"ruff format {files}\"\nstage = \"edit\"")},
			custom: []string{"ruff"},
		},
		{name: "a directory with nothing in it", files: map[string]string{"README.md": "x\n"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			builtins, custom, err := gate.Checks(project(t, tt.files))
			require.NoError(t, err)
			assert.Equal(t, tt.builtins, builtins)
			assert.Equal(t, tt.custom, custom)
		})
	}
}
