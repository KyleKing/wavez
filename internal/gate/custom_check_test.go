package gate_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/gate"
)

func check(name, body string) string {
	return "[[check]]\nname = \"" + name + "\"\nglobs = [\"*.txt\"]\n" + body + "\n"
}

func TestRun_CustomChecks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		config   string
		changed  []string
		stage    gate.Stage
		wants    []want
		blocking bool
	}{
		{
			name: "a passing command passes", config: check("ok", `command = "true"`),
			changed: []string{"a.txt"}, stage: gate.StageStop, wants: []want{{"ok", gate.StatusPass, ""}},
		},
		{
			name:    "a failing command fails with its output",
			config:  check("bad", `command = "echo a.txt:1:1: wrong; exit 3"`),
			changed: []string{"a.txt"}, stage: gate.StageStop, blocking: true,
			wants: []want{{"bad", gate.StatusFail, "a.txt:1:1: wrong"}},
		},
		{
			name:    "an advisory failure does not block",
			config:  check("soft", "command = \"echo meh >&2; false\"\nlevel = \"advise\""),
			changed: []string{"a.txt"}, stage: gate.StageStop, wants: []want{{"soft", gate.StatusFail, "meh"}},
		},
		{
			name: "a missing program abstains", config: check("gone", `command = "wavez-no-such-tool {files}"`),
			changed: []string{"a.txt"}, stage: gate.StageStop,
			wants: []want{{"gone", gate.StatusAbstained, "wavez-no-such-tool"}},
		},
		{
			name: "a timeout abstains", config: check("slow", "command = \"sleep 30\"\ntimeout = \"100ms\""),
			changed: []string{"a.txt"}, stage: gate.StageStop,
			wants: []want{{"slow", gate.StatusAbstained, "timed out after 100ms"}},
		},
		{
			name: "a check runs only at its stage", config: check("stop", `command = "false"`),
			changed: []string{"a.txt"}, stage: gate.StageEdit,
		},
		{
			name:    "an edit-stage check runs at edit",
			config:  check("edit", "command = \"false\"\nstage = \"edit\""),
			changed: []string{"a.txt"}, stage: gate.StageEdit, blocking: true,
			wants: []want{{"edit", gate.StatusFail, ""}},
		},
		{
			name: "a check nothing matches is absent", config: check("txt", `command = "false"`),
			changed: []string{"a.md"}, stage: gate.StageStop,
		},
		{
			name: "files are quoted for the shell", config: check("quote", `command = "cat {files}"`),
			changed: []string{"it's here.txt", "a.txt"}, stage: gate.StageStop,
			wants: []want{{"quote", gate.StatusPass, ""}},
		},
		{
			name:    "a deleted file is not handed to a command that takes files",
			config:  check("del", `command = "cat {files}"`),
			changed: []string{"deleted.txt"}, stage: gate.StageStop,
		},
		{
			name: "a deleted file still selects a command that takes none", config: check("all", `command = "true"`),
			changed: []string{"deleted.txt"}, stage: gate.StageStop, wants: []want{{"all", gate.StatusPass, ""}},
		},
		{
			name:    "checks report in file order",
			config:  check("first", `command = "sleep 0.2; false"`) + check("second", `command = "true"`),
			changed: []string{"a.txt"}, stage: gate.StageStop, blocking: true,
			wants: []want{{"first", gate.StatusFail, ""}, {"second", gate.StatusPass, ""}},
		},
		{
			name: "paths that leave the root select nothing", config: check("out", `command = "false"`),
			changed: []string{"../a.txt", "/etc/a.txt", "."}, stage: gate.StageStop,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := project(t, map[string]string{".wavez.toml": tt.config, "a.txt": "a\n", "it's here.txt": "b\n"})
			report := runGate(t.Context(), t, root, tt.stage, tt.changed)

			expect(t, report, tt.wants...)

			if report.Failed() != tt.blocking {
				t.Errorf("Failed() = %v, want %v", report.Failed(), tt.blocking)
			}
		})
	}
}

func TestRun_FixRunsFirstAndReportsWhatItRewrote(t *testing.T) {
	t.Parallel()

	// The fixer repairs a.txt and leaves b.txt, which already passes, alone.
	// The check fails on any file still holding "bad", so it passes only if
	// the fixer ran before it.
	root := project(t, map[string]string{
		".wavez.toml": check("fixable",
			"command = \"! grep -l bad {files}\"\nfix = \"sed -i.bak s/bad/good/ {files}; exit 1\""),
		"a.txt": "bad\n",
		"b.txt": "good\n",
	})

	report := runGate(t.Context(), t, root, gate.StageStop, []string{"a.txt", "b.txt"})

	expect(t, report, want{"fixable", gate.StatusPass, ""})

	if got := report.Results[0].Fixed; !slices.Equal(got, []string{"a.txt"}) {
		t.Errorf("Fixed = %v, want only a.txt", got)
	}

	if got := readFile(t, root, "a.txt"); got != "good\n" {
		t.Errorf("a.txt = %q", got)
	}

	if !strings.Contains(report.Render(), "a.txt") {
		t.Errorf("Render() does not name the rewritten file:\n%s", report.Render())
	}
}

func TestRun_AbstainsWhenCancelledOrWithoutAShell(t *testing.T) {
	t.Parallel()

	files := map[string]string{
		".wavez.toml": check("x", "command = \"false\"\nfix = \"false\""),
		"a.txt":       "a\n",
	}

	t.Run("canceled", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		report := runGate(ctx, t, project(t, files), gate.StageStop, []string{"a.txt"})
		expect(t, report, want{"x", gate.StatusAbstained, "canceled"})
	})

	t.Run("no shell", func(t *testing.T) {
		t.Parallel()

		report := runGate(t.Context(), t, project(t, files), gate.StageStop, []string{"a.txt"}, without("sh"))
		expect(t, report, want{"x", gate.StatusAbstained, "sh not on PATH"})
	})
}

func TestRun_BoundsALongFailure(t *testing.T) {
	t.Parallel()

	root := project(t, map[string]string{
		".wavez.toml": check("noisy", `command = "seq 1 5000; exit 1"`),
		"a.txt":       "a\n",
	})

	report := runGate(t.Context(), t, root, gate.StageStop, []string{"a.txt"})
	expect(t, report, want{"noisy", gate.StatusFail, "bytes cut"})

	if n := len(report.Results[0].Summary); n > 3100 {
		t.Errorf("summary is %d bytes", n)
	}
}

func TestRun_InputsThatSelectNothingOrCannotLoad(t *testing.T) {
	t.Parallel()

	t.Run("no changed files", func(t *testing.T) {
		t.Parallel()

		report := runGate(t.Context(), t, t.TempDir(), gate.StageStop, nil)
		expect(t, report)
	})

	t.Run("a project with no go.mod and no config has no checks", func(t *testing.T) {
		t.Parallel()

		root := project(t, map[string]string{"a.go": "package a\n"})
		expect(t, runGate(t.Context(), t, root, gate.StageStop, []string{"a.go"}))
		expect(t, runGate(t.Context(), t, root, gate.StageEdit, []string{"a.go"}))
	})

	t.Run("a broken config is an error", func(t *testing.T) {
		t.Parallel()

		root := project(t, map[string]string{".wavez.toml": "nope = 1\n"})

		_, err := gate.New().Run(t.Context(), root, []string{"a.txt"}, gate.StageStop)
		if err == nil || !strings.Contains(err.Error(), "nope") {
			t.Errorf("got %v, want an error naming the key", err)
		}
	})

	t.Run("a root that does not exist is an error", func(t *testing.T) {
		t.Parallel()

		_, err := gate.New().Run(t.Context(), "/no/such/root", []string{"a.txt"}, gate.StageStop)
		if err == nil {
			t.Error("got no error")
		}
	})
}
