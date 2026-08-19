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

			got := guard.ExecutedScripts(tt.command, guard.Env{ProjectRoot: root})
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

	got := guard.Classify(script, guard.Env{ProjectRoot: "/proj"})
	if got.Verdict != guard.Refuse {
		t.Errorf("Verdict = %q, want %q for a script whose third line is `rm -rf /`", got.Verdict, guard.Refuse)
	}
}

// A target hidden behind a variable can join onto the project root and read
// as inside it, letting `rm -rf $HOME/thing` slip through as allowed. What the
// guard cannot reduce to one location it refuses to call safe.
func TestClassifyExpandsDestructiveTargets(t *testing.T) {
	t.Parallel()

	env := guard.Env{ProjectRoot: "/repo", Home: "/home/u", TempDir: "/tmp"}

	tests := []struct {
		name    string
		command string
		want    guard.Verdict
	}{
		{name: "home in a variable", command: "rm -rf $HOME/thing", want: guard.Refuse},
		{name: "home in a braced variable", command: "rm -rf ${HOME}/thing", want: guard.Refuse},
		{name: "home as a tilde path", command: "rm -rf ~/thing", want: guard.Refuse},
		{name: "temp dir in a variable", command: "rm -rf $TMPDIR/thing", want: guard.Refuse},
		{name: "the project root through PWD", command: "rm -rf $PWD", want: guard.Refuse},
		{name: "inside the project through PWD", command: "rm -rf $PWD/build", want: guard.Allow},
		{name: "an unknown variable does not resolve", command: "rm -rf $BUILD_DIR", want: guard.NeedsApproval},
		{name: "a command substitution does not resolve", command: "rm -rf $(cat f)", want: guard.NeedsApproval},
		{name: "a glob does not resolve", command: "rm -rf build/*", want: guard.NeedsApproval},
		{name: "a plain path inside is still allowed", command: "rm -rf /repo/build", want: guard.Allow},
		{name: "a variable that only shares a prefix", command: "rm -rf $HOMEWORK", want: guard.NeedsApproval},
		{name: "chmod hides home behind a variable too", command: "chmod -R 777 $HOME/x", want: guard.Refuse},
		{name: "and chown", command: "chown -R me $HOME/x", want: guard.Refuse},
		{name: "chmod inside the project is allowed", command: "chmod +x /repo/s.sh", want: guard.Allow},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := guard.Classify(tt.command, env)
			if got.Verdict != tt.want {
				t.Errorf("Classify(%q) = %s, want %s (%s)", tt.command, got.Verdict, tt.want, got.Reason)
			}
		})
	}
}
