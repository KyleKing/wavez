package guard_test

import (
	"slices"
	"testing"

	"github.com/kyleking/wavez/internal/guard"
)

func TestExecutedScripts(t *testing.T) {
	t.Parallel()

	const root = "/proj"

	tests := []struct {
		name    string
		command string
		want    []string
	}{
		{name: "invoked by relative path", command: "./setup.sh", want: []string{"setup.sh"}},
		{name: "invoked in a subdirectory", command: "./scripts/run.sh", want: []string{"scripts/run.sh"}},
		{name: "handed to an interpreter", command: "sh setup.sh", want: []string{"setup.sh"}},
		{name: "interpreter with a flag first", command: "bash -x setup.sh", want: []string{"setup.sh"}},
		{name: "python script", command: "python3 tools/gen.py", want: []string{"tools/gen.py"}},
		{name: "sourced into the shell", command: ". ./env.sh", want: []string{"env.sh"}},
		{name: "source builtin", command: "source env.sh", want: []string{"env.sh"}},
		{name: "absolute path inside the project", command: "/proj/setup.sh", want: []string{"setup.sh"}},
		{name: "each stage of a pipeline", command: "./a.sh | ./b.sh", want: []string{"a.sh", "b.sh"}},
		{name: "each command of a sequence", command: "./a.sh && ./b.sh", want: []string{"a.sh", "b.sh"}},
		{name: "the same script twice is named once", command: "./a.sh && ./a.sh", want: []string{"a.sh"}},
		{name: "a bare name is on PATH, not ours", command: "ls -la"},
		{name: "a path outside the project", command: "/usr/local/bin/thing"},
		{name: "escaping the project root upward", command: "../outside.sh"},
		{name: "an interpreter with no script", command: "python3 -c 'print(1)'"},
		{name: "an argument to the script is not itself run", command: "sh run.sh other.sh", want: []string{"run.sh"}},
		{name: "empty", command: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := guard.ExecutedScripts(tt.command, root)
			if !slices.Equal(got, tt.want) {
				t.Errorf("ExecutedScripts(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}

// A newline separates commands the way `;` does, so a script's later lines
// are classified and not swallowed into the first one.
func TestClassifyReadsEveryLineOfAScript(t *testing.T) {
	t.Parallel()

	script := "#!/bin/sh\necho hello\nrm -rf /\n"

	got := guard.Classify(script, "/proj")
	if got.Verdict != guard.Refuse {
		t.Errorf("Verdict = %q, want %q for a script whose third line is `rm -rf /`", got.Verdict, guard.Refuse)
	}
}
