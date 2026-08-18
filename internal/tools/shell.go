package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/kyleking/wavez/internal/guard"
	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/sandbox"
	"github.com/kyleking/wavez/internal/tool"
)

var shellSchema = buildSchema(map[string]schemaProperty{
	"command": {
		Type: schemaTypeString,
		Description: "A shell command line to run in the project root. Destructive patterns " +
			"(rm -rf outside the root, force pushes, disk formatting) are refused before " +
			"anything runs; do not attempt to work around a refusal.",
	},
}, "command")

const (
	shellHeadLines = 20
	shellTailLines = 20
)

// reasonNoScript is the reason attached to a path that named no readable
// file, which is not suspicious: a command may name a file it is about to
// create, or one on PATH that merely looks like a path.
const reasonNoScript = "names no readable file in the project"

// Shell runs a command line in the sandbox after two deterministic checks,
// in a fixed order that no permission answer can shorten: guard.Classify
// first, since a Refuse verdict must be unreachable regardless of what the
// permission.Gate would say; the Gate second, consulted only when the
// verdict needs approval, since an Allow verdict runs without a prompt and
// a Refuse verdict never reaches the Gate at all; sandbox.Exec last, so
// nothing runs before both checks have cleared it.
type Shell struct {
	gate       permission.Gate
	root       string
	sessionTmp string
	threadID   string
}

// NewShell builds a Shell tool scoped to root and sessionTmp, gating
// approval-worthy commands through gate. The threadID identifies the
// thread in permission.Request.
func NewShell(root, sessionTmp, threadID string, gate permission.Gate) *Shell {
	return &Shell{root: root, sessionTmp: sessionTmp, threadID: threadID, gate: gate}
}

// Name implements tool.Tool.
func (*Shell) Name() string { return "shell" }

// Description implements tool.Tool.
func (*Shell) Description() string {
	return "Run a shell command in the project root. Destructive commands may be refused " +
		"outright or require user approval before they run. Output is trimmed to its first " +
		"and last lines when long; the exit code is always reported."
}

// Schema implements tool.Tool.
func (*Shell) Schema() json.RawMessage { return shellSchema }

type shellInput struct {
	Command string `json:"command"`
}

// Run implements tool.Tool.
func (s *Shell) Run(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, fmt.Errorf("shell: %w", err)
	}

	var in shellInput
	if err := json.Unmarshal(input, &in); err != nil {
		return tool.Errorf("invalid input: %v", err), nil
	}

	verdict := s.classify(in.Command)

	switch verdict.Verdict {
	case guard.Refuse:
		return tool.Errorf("refused: %s (%q)", verdict.Reason, verdict.Fragment), nil
	case guard.NeedsApproval:
		decision, err := s.gate.Ask(ctx, permission.Request{
			ThreadID: s.threadID,
			Tool:     s.Name(),
			Action:   "run",
			Detail:   in.Command,
			Key:      approvalKey(in.Command),
			Reason:   verdict.Reason,
		})
		if err != nil {
			return tool.Errorf("requesting approval: %v", err), nil
		}

		if decision == permission.Deny {
			return tool.Errorf("denied: %s (%q)", verdict.Reason, verdict.Fragment), nil
		}
	case guard.Allow:
	}

	result, err := sandbox.Exec(ctx, s.root, s.sessionTmp, "sh", "-c", in.Command)
	if err != nil {
		return tool.Result{}, fmt.Errorf("shell: %w", err)
	}

	return tool.Result{Content: formatShellResult(result)}, nil
}

// maxScriptBytes bounds how much of a script the guard reads. A file
// larger than this is not a script a run just wrote, and reading it whole
// would put an unbounded string through the classifier.
const maxScriptBytes = 64 * 1024

// classify judges the command, then judges the contents of every project
// script it would run and takes the worst of them.
//
// Running `./setup.sh` says nothing about what happens next, so classifying
// only the command line would let a run write anything into a file and then
// execute it past a guard that never saw it. Creating a file is not the
// dangerous step and is not gated; this is the step that is.
//
// A script the guard cannot read is approval-worthy rather than allowed,
// for the same reason an unparsable fragment is: this guard fails closed.
func (s *Shell) classify(command string) guard.Result {
	verdict := guard.Classify(command, s.root)
	if verdict.Verdict == guard.Refuse {
		return verdict
	}

	for _, rel := range guard.ExecutedScripts(command, s.root) {
		inner := s.classifyScript(rel)
		if inner.Verdict.Worse(verdict.Verdict) {
			verdict = inner
		}
	}

	return verdict
}

func (s *Shell) classifyScript(rel string) guard.Result {
	abs, err := resolvePath(s.root, rel)
	if err != nil {
		return guard.Result{Verdict: guard.Allow, Reason: reasonNoScript, Fragment: rel}
	}

	info, err := os.Lstat(abs)
	if err != nil || !info.Mode().IsRegular() {
		return guard.Result{Verdict: guard.Allow, Reason: reasonNoScript, Fragment: rel}
	}

	if info.Size() > maxScriptBytes {
		return guard.Result{
			Verdict:  guard.NeedsApproval,
			Reason:   "runs " + rel + ", too large to read before running it",
			Fragment: rel,
		}
	}

	body, err := os.ReadFile(abs) //nolint:gosec // abs is resolved inside the project root above
	if err != nil {
		return guard.Result{
			Verdict:  guard.NeedsApproval,
			Reason:   "runs " + rel + ", which could not be read before running it",
			Fragment: rel,
		}
	}

	inner := guard.Classify(string(body), s.root)
	if inner.Verdict == guard.Allow {
		return inner
	}

	return guard.Result{
		Verdict:  inner.Verdict,
		Reason:   "runs " + rel + ", which " + inner.Reason,
		Fragment: inner.Fragment,
	}
}

func approvalKey(command string) string {
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return command
	}

	return fields[0]
}

func formatShellResult(result sandbox.Result) string {
	var b strings.Builder

	fmt.Fprintf(&b, "exit code: %d\n", result.ExitCode)
	fmt.Fprintf(&b, "stdout:\n%s", trimOutput(result.Stdout))

	if result.Stderr != "" {
		fmt.Fprintf(&b, "\nstderr:\n%s", trimOutput(result.Stderr))
	}

	return b.String()
}

// trimOutput keeps the first and last shellHeadLines/shellTailLines lines of
// s, noting how many lines were dropped in between.
func trimOutput(s string) string {
	if s == "" {
		return "(empty)"
	}

	lines := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
	if len(lines) <= shellHeadLines+shellTailLines {
		return strings.Join(lines, "\n")
	}

	dropped := len(lines) - shellHeadLines - shellTailLines
	head := lines[:shellHeadLines]
	tail := lines[len(lines)-shellTailLines:]

	out := make([]string, 0, shellHeadLines+shellTailLines+1)
	out = append(out, head...)
	out = append(out, fmt.Sprintf("... [%d lines omitted] ...", dropped))
	out = append(out, tail...)

	return strings.Join(out, "\n")
}
