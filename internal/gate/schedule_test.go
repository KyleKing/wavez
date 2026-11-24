package gate_test

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/gate"
)

// meet is a check command that marks its own start under dir, waits for
// other's mark, and fails if it does not come within a minute: it passes
// only when the two checks ran at the same time.
func meet(dir, self, other string) string {
	mark, wait := filepath.Join(dir, self), filepath.Join(dir, other)

	return fmt.Sprintf("touch '%s'; i=0; while [ ! -e '%s' ]; do i=$((i+1)); "+
		"[ $i -gt 600 ] && exit 1; sleep 0.1; done", mark, wait)
}

// logged is a check command that appends its start and end to log with a
// pause between, so two checks running at once interleave there.
func logged(log, name string) string {
	return fmt.Sprintf("echo start %[2]s >> '%[1]s'; sleep 0.3; echo end %[2]s >> '%[1]s'", log, name)
}

func checkCommand(name, command, extra string) string {
	return fmt.Sprintf("[[check]]\nname = %q\nglobs = [\"*.txt\"]\ncommand = %q\n%s\n", name, command, extra)
}

func TestRun_ChecksRunSideBySide(t *testing.T) {
	t.Parallel()

	marks := t.TempDir()
	config := "parallel = 2\n" +
		checkCommand("one", meet(marks, "one", "two"), "") +
		checkCommand("two", meet(marks, "two", "one"), "")

	root := project(t, map[string]string{".wavez.toml": config, "a.txt": "x"})
	report := runGate(t.Context(), t, root, gate.StageStop, []string{"a.txt"})

	expect(t, report, want{"one", gate.StatusPass, ""}, want{"two", gate.StatusPass, ""})
}

func TestRun_ChecksThatMustNotOverlapDoNot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		header string
		extra  map[string]string
		alone  []string
	}{
		{name: "parallel = 1 runs one at a time", header: "parallel = 1\n", alone: []string{"a", "b", "c"}},
		{
			name: "an exclusive check runs alone", header: "parallel = 4\n",
			extra: map[string]string{"b": "exclusive = true"}, alone: []string{"b"},
		},
		{
			name: "an exclusive first check runs alone", header: "parallel = 4\n",
			extra: map[string]string{"a": "exclusive = true"}, alone: []string{"a"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			log := filepath.Join(t.TempDir(), "log")
			var config strings.Builder

			config.WriteString(tt.header)

			for _, name := range []string{"a", "b", "c"} {
				config.WriteString(checkCommand(name, logged(log, name), tt.extra[name]))
			}

			root := project(t, map[string]string{".wavez.toml": config.String(), "a.txt": "x"})
			report := runGate(t.Context(), t, root, gate.StageStop, []string{"a.txt"})

			if report.Failed() || report.Abstained() {
				t.Fatalf("got %+v", report.Results)
			}

			events := strings.Split(strings.TrimSpace(readFile(t, filepath.Dir(log), "log")), "\n")
			for _, name := range tt.alone {
				at := slices.Index(events, "start "+name)
				if at < 0 || at+1 >= len(events) || events[at+1] != "end "+name {
					t.Errorf("%s did not run alone:\n%s", name, strings.Join(events, "\n"))
				}
			}
		})
	}
}
