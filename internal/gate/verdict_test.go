package gate_test

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/gate"
)

// countingRun is a check command that appends a line to count each time it
// runs and then runs body.
func countingRun(count, body string) string {
	return fmt.Sprintf("echo ran >> '%s'; %s", count, body)
}

func runsIn(t *testing.T, count string) int {
	t.Helper()

	data, err := readOptional(count)
	if err != nil {
		t.Fatalf("reading %s: %v", count, err)
	}

	return strings.Count(data, "ran")
}

// step is one run in a cache scenario: an edit to make first, and whether
// the command should then have run again.
type step struct {
	edit    map[string]string
	ran     bool
	cached  bool
	noCache bool
}

func TestRun_ConfiguredCheckPassIsCachedByContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		inputs string
		body   string
		status gate.Status
		steps  []step
	}{
		{
			name: "no edit reuses the pass", inputs: `inputs = ["cfg.ini"]`, body: "true", status: gate.StatusPass,
			steps: []step{{ran: true}, {cached: true}},
		},
		{
			name: "an edit to a declared input runs it again", inputs: `inputs = ["cfg.ini"]`, body: "true",
			status: gate.StatusPass,
			steps:  []step{{ran: true}, {edit: map[string]string{"cfg.ini": "y"}, ran: true}, {cached: true}},
		},
		{
			name: "an edit to another file the globs match runs it again", inputs: `inputs = []`, body: "true",
			status: gate.StatusPass,
			steps:  []step{{ran: true}, {edit: map[string]string{"other.txt": "z"}, ran: true}},
		},
		{
			name: "an edit to a file it did not declare is not seen", inputs: `inputs = []`, body: "true",
			status: gate.StatusPass,
			steps:  []step{{ran: true}, {edit: map[string]string{"cfg.ini": "y"}, cached: true}},
		},
		{
			name: "a check without inputs is never cached", body: "true", status: gate.StatusPass,
			steps: []step{{ran: true}, {ran: true}},
		},
		{
			name: "a failure is never cached", inputs: `inputs = []`, body: "exit 3", status: gate.StatusFail,
			steps: []step{{ran: true}, {ran: true}},
		},
		{
			name: "an abstention is never cached", inputs: `inputs = []`, body: "exit 127",
			status: gate.StatusAbstained, steps: []step{{ran: true}, {ran: true}},
		},
		{
			name: "the cache can be turned off", inputs: `inputs = []`, body: "true", status: gate.StatusPass,
			steps: []step{{ran: true}, {ran: true, noCache: true}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			count := filepath.Join(t.TempDir(), "count")
			config := checkCommand("c", countingRun(count, tt.body), tt.inputs)
			root := project(t, map[string]string{
				".wavez.toml": config, "a.txt": "x", "other.txt": "o", "cfg.ini": "x",
			})

			for i, s := range tt.steps {
				for name, body := range s.edit {
					writeFile(t, root, name, body)
				}

				var opts []gate.Option
				if s.noCache {
					opts = append(opts, gate.WithoutCache())
				}

				before := runsIn(t, count)
				report := runGate(t.Context(), t, root, gate.StageStop, []string{"a.txt"}, opts...)
				res := report.Results[0]

				if ran := runsIn(t, count) > before; ran != s.ran || res.Cached != s.cached || res.Status != tt.status {
					t.Errorf("step %d: ran %v cached %v status %s, want ran %v cached %v status %s",
						i, ran, res.Cached, res.Status, s.ran, s.cached, tt.status)
				}
			}
		})
	}
}

func TestRun_CachedPassSkipsTheFixer(t *testing.T) {
	t.Parallel()

	count := filepath.Join(t.TempDir(), "count")
	config := checkCommand("c", "true", fmt.Sprintf("inputs = []\nfix = %q", countingRun(count, "true")))
	root := project(t, map[string]string{".wavez.toml": config, "a.txt": "x"})

	for range 2 {
		runGate(t.Context(), t, root, gate.StageStop, []string{"a.txt"})
	}

	if got := runsIn(t, count); got != 1 {
		t.Errorf("the fixer ran %d times, want 1", got)
	}
}

// countingLinter writes a stand-in golangci-lint that counts its runs and
// prints output.
func countingLinter(t *testing.T, root, count, output, status string) gate.Option {
	t.Helper()

	writeFile(t, root, ".fake/out.txt", output)
	script := "#!/bin/sh\necho \"$@\" | sed 's/^/ran /' >> '%s'\ncat \"$(dirname \"$0\")/out.txt\"\nexit %s\n"
	writeFile(t, root, ".fake/golangci-lint", fmt.Sprintf(script, count, status))

	return replacing("golangci-lint", filepath.Join(root, ".fake", "golangci-lint"))
}

func TestRun_LintPassIsCachedPerPackage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		edit map[string]string
		name string
		// linted is the package patterns the second run hands the linter,
		// "" when it does not run.
		linted  string
		changed []string
		cached  bool
	}{
		{name: "nothing changed", changed: []string{"b/b.go"}, cached: true},
		{
			name: "a package it imports changed", edit: map[string]string{"a/a.go": srcAThree},
			changed: []string{"b/b.go"}, linted: "./b",
		},
		{
			name: "a package it does not import changed", edit: map[string]string{"lone/l.go": srcLone + "\n"},
			changed: []string{"b/b.go"}, cached: true,
		},
		{
			name: "the linter configuration changed", edit: map[string]string{".golangci.toml": "# stricter\n"},
			changed: []string{"b/b.go"}, linted: "./b",
		},
		{
			name: "a second package joins the change set", changed: []string{"b/b.go", "lone/l.go"}, linted: "./lone",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			count := filepath.Join(t.TempDir(), "count")
			root := project(t, module(map[string]string{
				".wavez.toml": "disable = [\"build\", \"test\"]\n", ".golangci.toml": "# lint\n",
			}))
			opt := countingLinter(t, root, count, "", "0")

			first := runGate(t.Context(), t, root, gate.StageStop, []string{"b/b.go"}, opt)
			expect(t, first, want{"lint", gate.StatusPass, ""})

			for name, body := range tt.edit {
				writeFile(t, root, name, body)
			}

			before := runsIn(t, count)
			second := runGate(t.Context(), t, root, gate.StageStop, tt.changed, opt)
			expect(t, second, want{"lint", gate.StatusPass, ""})

			log, err := readOptional(count)
			if err != nil {
				t.Fatalf("reading the linter's log: %v", err)
			}

			lines := strings.Split(strings.TrimSpace(log), "\n")
			ranAgain := runsIn(t, count) != before

			switch {
			case tt.linted == "" && ranAgain:
				t.Errorf("the linter ran again: %s", log)
			case tt.linted != "" && (!ranAgain || !strings.HasSuffix(lines[len(lines)-1], " "+tt.linted)):
				t.Errorf("want the second run to lint only %s, got:\n%s", tt.linted, log)
			}

			if second.Results[0].Cached != tt.cached {
				t.Errorf("cached = %v, want %v", second.Results[0].Cached, tt.cached)
			}
		})
	}
}

func TestRun_LintFindingIsNotCached(t *testing.T) {
	t.Parallel()

	count := filepath.Join(t.TempDir(), "count")
	root := project(t, module(map[string]string{
		".wavez.toml": "disable = [\"build\", \"test\"]\n", ".golangci.toml": "# lint\n",
	}))
	opt := countingLinter(t, root, count, "b/b.go:3:1: wrong (revive)\n", "1")

	for range 2 {
		report := runGate(t.Context(), t, root, gate.StageStop, []string{"b/b.go"}, opt)
		expect(t, report, want{"lint", gate.StatusFail, "b/b.go:3:1: wrong"})
	}

	if got := runsIn(t, count); got != 2 {
		t.Errorf("the linter ran %d times, want 2", got)
	}
}

// readOptional reads a file that a command may not have written yet.
func readOptional(name string) (string, error) {
	data, err := os.ReadFile(name) //nolint:gosec // a file under the test's own temporary directory
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}

	return string(data), err
}
