package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kyleking/wavez/internal/guard"
	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/sandbox"
	"github.com/kyleking/wavez/internal/stakes"
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

// Shell runs a command line in the sandbox after two deterministic checks,
// in a fixed order that no permission answer can shorten: guard.Classify
// first, since a Refuse verdict must be unreachable regardless of what the
// permission.Gate would say; the Gate second, consulted only when the
// verdict needs approval, since an Allow verdict runs without a prompt and
// a Refuse verdict never reaches the Gate at all; sandbox.Exec last, so
// nothing runs before both checks have cleared it.
type Shell struct {
	gate       permission.Gate
	changes    *stakes.ChangeSet
	root       string
	sessionTmp string
	threadID   string
}

// NewShell builds a Shell tool scoped to root and sessionTmp, gating
// approval-worthy commands through gate. The threadID identifies the
// thread in permission.Request. The changes set is what the run has edited
// so far, scored into each prompt so approving a command blind costs the
// user the whole run's evidence rather than the command's alone; a nil one
// renders every edit-derived signal as unknown.
func NewShell(root, sessionTmp, threadID string, gate permission.Gate, changes *stakes.ChangeSet) *Shell {
	return &Shell{root: root, sessionTmp: sessionTmp, threadID: threadID, gate: gate, changes: changes}
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

	verdict := guard.Classify(in.Command, s.root)

	switch verdict.Verdict {
	case guard.Refuse:
		return tool.Errorf("refused: %s (%q)", verdict.Reason, verdict.Fragment), nil
	case guard.NeedsApproval:
		score := stakes.Compute(stakes.Input{
			ProjectRoot: s.root,
			Guard:       &verdict.Verdict,
			Edits:       s.changes.Edits(),
		})
		decision, err := s.gate.Ask(ctx, permission.Request{
			ThreadID: s.threadID,
			Tool:     s.Name(),
			Action:   "run",
			Detail:   in.Command,
			Key:      approvalKey(in.Command),
			Stakes:   &score,
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
