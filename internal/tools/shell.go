package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kyleking/wavez/internal/guard"
	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/reduce"
	"github.com/kyleking/wavez/internal/sandbox"
	"github.com/kyleking/wavez/internal/tool"
)

var shellSchema = buildSchema(map[string]schemaProperty{
	"command": {
		Type: schemaTypeString,
		Description: "A shell command line to run in the project root. Do not attempt to " +
			"work around a refusal.",
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
	deps       deps
	root       string
	sessionTmp string
	threadID   string
	env        guard.Env
}

// NewShell builds a Shell tool scoped to root and sessionTmp, gating
// approval-worthy commands through gate. The threadID identifies the
// thread in permission.Request.
func NewShell(root, sessionTmp, threadID string, gate permission.Gate, opts ...Option) *Shell {
	// Read once here rather than inside the guard: a verdict has to be a
	// function of its inputs, so the machine's home and temp directory are
	// arguments to it and not something it looks up for itself.
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}

	return &Shell{
		root: root, sessionTmp: sessionTmp, threadID: threadID, gate: gate, deps: newDeps(opts),
		env: guard.Env{
			ProjectRoot: root, Home: home, TempDir: os.TempDir(), ColocatedJJ: colocatedJJ(root),
		},
	}
}

// colocatedJJ reports a project jj owns the working copy of. Read once at
// construction for the same reason the home directory is: the guard decides
// from its inputs and looks nothing up for itself.
func colocatedJJ(root string) bool {
	info, err := os.Stat(filepath.Join(root, ".jj"))

	return err == nil && info.IsDir()
}

// Name implements tool.Tool.
func (*Shell) Name() string { return "shell" }

// Description implements tool.Tool.
func (*Shell) Description() string {
	return "Run a shell command in the project root. Destructive commands may be refused " +
		"outright or require user approval before they run. Long output is reduced to the " +
		"lines that name a failure, and what was dropped is stated, so piping through head, " +
		"tail, or grep to shorten it wastes a turn. The exit code is always reported."
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

	if answer, ok := s.alreadyKnown(in.Command); ok {
		return tool.Result{Content: answer}, nil
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

	// A command that writes takes the leases covering what it writes, since
	// the edit tools are not the only way a thread changes the tree.
	release, err := s.deps.holdAll(ctx, existingDirs(guard.WriteTargets(in.Command, s.env)))
	if err != nil {
		return tool.Errorf("%v", err), nil
	}
	defer release()

	result, err := sandbox.Exec(ctx, s.root, s.sessionTmp, "sh", "-c", in.Command)
	if err != nil {
		return tool.Result{}, fmt.Errorf("shell: %w", err)
	}

	return tool.Result{Content: formatShellResult(result)}, nil
}

// alreadyKnown answers a command whose answer the harness is already
// holding, rather than spending a subprocess and a turn to rediscover it.
func (s *Shell) alreadyKnown(command string) (string, bool) {
	if answer, ok := s.alreadyChecked(command); ok {
		return answer, true
	}

	return s.alreadyWritten(command)
}

// alreadyWritten answers a read-only version-control command with what this
// run has written. The system prompt has told runs not to call git or jj
// since the harness took over checkpointing, and 24 of 278 logged shell
// calls called one anyway, every time to ask a question answered here.
func (s *Shell) alreadyWritten(command string) (string, bool) {
	if s.deps.changes == nil || !guard.VCSInspect(command) {
		return "", false
	}

	changed := s.deps.changes.Changed()
	if len(changed) == 0 {
		return "Not run: version control is the harness's job. You have changed nothing " +
			"this run.", true
	}

	var b strings.Builder

	b.WriteString("Not run: version control is the harness's job. You have changed " +
		"these files this run:\n")

	for _, c := range dedupeChanges(changed) {
		fmt.Fprintf(&b, "  %s (+%d/-%d)\n", c.Path, c.Added, c.Removed)
	}

	return strings.TrimSuffix(b.String(), "\n"), true
}

// dedupeChanges folds repeated writes to one path into one row carrying
// every line the run added or removed there, since a model asking what it
// changed wants the file list and not the edit history.
func dedupeChanges(changes []tool.Change) []tool.Change {
	order := make([]string, 0, len(changes))
	total := make(map[string]tool.Change, len(changes))

	for _, c := range changes {
		seen, ok := total[c.Path]
		if !ok {
			order = append(order, c.Path)
			total[c.Path] = c

			continue
		}

		seen.Added += c.Added
		seen.Removed += c.Removed
		total[c.Path] = seen
	}

	out := make([]tool.Change, 0, len(order))
	for _, p := range order {
		out = append(out, total[p])
	}

	return out
}

// alreadyChecked answers a command that re-runs the project's checks from
// what the harness already knows, when it knows anything. This is not an
// error: the model asked a question the harness can answer, so it gets the
// answer rather than a refusal to look it up.
func (s *Shell) alreadyChecked(command string) (string, bool) {
	if s.deps.checks == nil {
		return "", false
	}

	name, ok := guard.ProjectCheck(command)
	if !ok {
		return "", false
	}

	status, ok := s.deps.checks.Status()
	if !ok {
		return "", false
	}

	answer := fmt.Sprintf("Not run: this runs %s, which the harness runs for you, and %s",
		name, status)
	advice := "Run one package's tests if you need to watch a single failure."

	if strings.Contains(status, "\n") {
		return answer + "\n\n" + advice, true
	}

	return answer + ". " + advice, true
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
	verdict := guard.Classify(command, s.env)
	if verdict.Verdict == guard.Refuse {
		return verdict
	}

	for _, rel := range guard.ExecutedScripts(command, s.env) {
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

	inner := guard.Classify(string(body), s.env)
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

// trimOutput reduces s to what names a failure, then caps whatever survives
// at shellHeadLines/shellTailLines. The reducer is what makes the cap safe:
// head and tail alone put the fixed windows at the two ends of a verbose test
// run, which is exactly where the assertion is not.
func trimOutput(s string) string {
	if s == "" {
		return "(empty)"
	}

	s = reduce.Output(s).Text

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
