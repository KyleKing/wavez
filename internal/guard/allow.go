package guard

import "strings"

// defaultAllowed names the commands that run without asking. Everything
// else needs approval, which inverts this package's original default: a
// rule list can only refuse what someone thought to name, and a probe
// against the pre-allowlist classifier let `security find-generic-password`,
// `nc`, `osascript`, `launchctl`, and `ssh-add -L` through with no prompt
// because no rule mentioned them.
//
// The list is what 177 logged shell calls across the replay corpus actually
// invoked (`go` led 123 pipeline stages, `grep` 66, `head` 32, `echo` 31),
// plus the read-only neighbors of each. Anything absent is one approval
// away and the answer is remembered for the thread, so a gap costs a prompt
// rather than a failed run.
//
// Shell interpreters are deliberately absent. A command already runs under
// `sh -c`, so a nested one is a way to hand the classifier a string it never
// reads.
var defaultAllowed = map[string]bool{
	"awk": true, "basename": true, "cat": true, "cd": true, "chmod": true, "chown": true, "cksum": true,
	"comm": true, "cp": true, "cut": true, "date": true, cmdDiff: true,
	"dirname": true, "du": true, "echo": true, "expr": true, "false": true,
	"file": true, "go": true, cmdGofmt: true, "golangci-lint": true, "grep": true,
	"head": true, "hk": true, "jq": true, "ln": true, "ls": true,
	cmdMise: true, "mkdir": true, "mktemp": true, "mv": true, "pkl": true,
	"printf": true, "pwd": true, "readlink": true, "realpath": true, "rg": true,
	"rm": true, "sed": true, "seq": true, "sort": true, "stat": true,
	"tail": true, "tee": true, "test": true, "touch": true, "tr": true,
	"tree": true, "true": true, "uniq": true, "wc": true, "which": true,
	"xargs": true, "yq": true,
}

// withoutAssignments drops the `NAME=value` prefix a stage may carry before
// the command it runs, so `GOFLAGS=-mod=mod go build` is judged as `go` and
// not as an unlisted command named `GOFLAGS=-mod=mod`.
func withoutAssignments(tokens []string) []string {
	for i, tok := range tokens {
		eq := strings.IndexByte(tok, '=')
		if eq <= 0 || strings.ContainsAny(tok[:eq], "/.-") {
			return tokens[i:]
		}
	}

	return nil
}

// reasonUnlisted explains an approval prompt raised only because nothing
// vouches for the command, which is a different thing from a rule objecting
// to it.
const reasonUnlisted = " is not on the list of commands that run without asking"

// allowed reports a program that runs without a prompt: one whose name
// ships on the list, one the project named, or a script named by a
// project-relative path.
//
// The last case is the one place this list defers to something else. A
// caller that runs a project script is required to classify the script's
// own contents (internal/tools.Shell does, in classifyScript), so judging
// `./setup.sh` by its filename would ask about a name while the thing that
// decides is the body.
func (e Env) allowed(prog, name string) bool {
	if projectRelative(prog) {
		return true
	}

	if defaultAllowed[name] {
		return true
	}

	for _, extra := range e.AllowedCommands {
		if strings.TrimSpace(extra) == name {
			return true
		}
	}

	return false
}

// projectRelative reports a program named by a path that stays inside the
// project, as opposed to a bare name looked up on PATH or an absolute one.
func projectRelative(prog string) bool {
	if !strings.Contains(prog, "/") || strings.HasPrefix(prog, "/") {
		return false
	}

	return !strings.HasPrefix(prog, "../") && !strings.Contains(prog, "/../")
}
