package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kyleking/wavez/internal/app"
	"github.com/kyleking/wavez/internal/lease"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/thread"
	"github.com/kyleking/wavez/internal/tool"
	"github.com/kyleking/wavez/internal/vcs"
)

// errNoRecordedCall reports a thread whose transcript holds no call the
// selection names.
var errNoRecordedCall = errors.New("no recorded tool call to repeat")

// recordedCall is one tool call as a finished run emitted it, beside the
// answer the harness gave it at the time.
type recordedCall struct {
	Answer  string
	Call    llm.ToolCall
	Turn    int
	IsError bool
}

// recallRun repeats one recorded tool call and prints what the harness
// answers now. It exists because every loop diagnosed by hand cost most of
// a session: read the transcript, reconstruct the answer, and reason about
// why the next attempt changed nothing. Re-running the call answers the
// only question a rewritten message raises, which is whether it says
// anything the old one did not.
//
// The tree the call meets is rebuilt by replaying every call before it,
// because an anchor that missed usually missed on what the earlier calls had
// already done. Every call rather than the editing ones: the h3 runs did
// most of their renaming through `sed` under the shell, so a prefix that
// skipped shell rebuilt a tree the target call never saw. That makes the
// prefix cost what the run's own commands cost, test runs included, and the
// trail on stderr is what says how far it has got.
func recallRun(ctx context.Context, root string, opt options) error {
	entries, err := thread.ReadHistory(app.ThreadLogDir(root), opt.recall)
	if err != nil {
		return fmt.Errorf("reading the transcript of %s: %w", opt.recall, err)
	}

	calls := recordedCalls(entries)
	if len(calls) == 0 {
		return fmt.Errorf("%w: %s kept no transcript, or made no call", errNoRecordedCall, opt.recall)
	}

	target, err := selectRecalled(calls, opt.recallTurn)
	if err != nil {
		return err
	}

	dir, done, err := recallWorkspace(ctx, root)
	if err != nil {
		return err
	}
	defer done()

	registry, closeApp, err := recallRegistry(ctx, dir, opt)
	if err != nil {
		return err
	}
	defer closeApp()

	// The edit tools take a lease per file, and a call with no holder in
	// context fails on that before it reaches the behavior being repeated.
	ctx = lease.WithHolder(ctx, opt.recall)

	replayPrefix(ctx, registry, calls, target)

	for _, c := range calls[target:] {
		if c.Turn != calls[target].Turn {
			break
		}

		reportRecall(ctx, registry, c)
	}

	return nil
}

// recordedCalls pairs every call in a transcript with the tool result that
// answered it. A call the run never got an answer for keeps an empty one,
// which is what the last turn of a run killed mid-call looks like.
func recordedCalls(entries []thread.TurnMessage) []recordedCall {
	answers := map[string]thread.TurnMessage{}

	for _, e := range entries {
		if e.Message.ToolCallID != "" {
			answers[e.Message.ToolCallID] = e
		}
	}

	var out []recordedCall

	for _, e := range entries {
		for _, c := range e.Message.ToolCalls {
			answer := answers[c.ID]
			out = append(out, recordedCall{
				Call: c, Turn: e.Turn, Answer: answer.Message.Content, IsError: answer.Message.IsError,
			})
		}
	}

	return out
}

// selectRecalled returns the index of the first call to repeat. Without a
// turn it is the first call the harness answered with an error, since that
// is the call anyone opens a transcript to look at.
func selectRecalled(calls []recordedCall, turn int) (int, error) {
	if turn > 0 {
		for i := range calls {
			if calls[i].Turn == turn {
				return i, nil
			}
		}

		return 0, fmt.Errorf("%w: no call on turn %d (calls on %s)",
			errNoRecordedCall, turn, strings.Join(recalledTurns(calls), ", "))
	}

	for i := range calls {
		if calls[i].IsError {
			return i, nil
		}
	}

	return 0, fmt.Errorf("%w: every call was answered without an error (calls on %s)",
		errNoRecordedCall, strings.Join(recalledTurns(calls), ", "))
}

func recalledTurns(calls []recordedCall) []string {
	seen := map[int]bool{}

	var turns []int

	for i := range calls {
		if !seen[calls[i].Turn] {
			seen[calls[i].Turn] = true

			turns = append(turns, calls[i].Turn)
		}
	}

	sort.Ints(turns)

	out := make([]string, 0, len(turns))
	for _, t := range turns {
		out = append(out, strconv.Itoa(t))
	}

	return out
}

// replayPrefix rebuilds the tree the target call met and prints how each
// step went, so a step that answers differently now is visible before the
// target's own answer is read as the change.
func replayPrefix(ctx context.Context, registry *tool.Registry, calls []recordedCall, target int) {
	started := time.Now()

	for _, c := range calls[:target] {
		res, err := runRecalled(ctx, registry, c.Call)

		fmt.Fprintf(os.Stderr, "  turn %d %s: %s\n", c.Turn, c.Call.Name, recallOutcome(res, err))
	}

	fmt.Fprintf(os.Stderr, "replayed %d calls in %s\n", target, time.Since(started).Round(time.Second))
}

func recallOutcome(res tool.Result, err error) string {
	switch {
	case err != nil:
		return "failed: " + err.Error()
	case res.IsError && res.Cause != "":
		return verdict(true) + " (" + string(res.Cause) + ")"
	default:
		return verdict(res.IsError)
	}
}

func runRecalled(ctx context.Context, registry *tool.Registry, call llm.ToolCall) (tool.Result, error) {
	t, err := registry.Get(call.Name)
	if err != nil {
		return tool.Result{}, fmt.Errorf("%s: %w", call.Name, err)
	}

	res, err := t.Run(ctx, call.Input)
	if err != nil {
		return res, fmt.Errorf("running %s: %w", call.Name, err)
	}

	return res, nil
}

// reportRecall prints the call, what the run was told at the time, and what
// the harness answers now. Both are printed whole: the question the tool
// exists for is whether the wording changed, and a truncated answer cannot
// settle it.
func reportRecall(ctx context.Context, registry *tool.Registry, c recordedCall) {
	fmt.Printf("turn %d, %s\n\narguments:\n%s\n\n", c.Turn, c.Call.Name, indent(string(c.Call.Input)))

	fmt.Printf("the run was told (%s):\n%s\n\n", verdict(c.IsError), indent(c.Answer))

	res, err := runRecalled(ctx, registry, c.Call)
	fmt.Printf("it is told now (%s):\n%s\n", recallOutcome(res, err), indent(res.Content))
}

func verdict(isError bool) string {
	if isError {
		return "error"
	}

	return "ok"
}

func indent(text string) string {
	if text == "" {
		return "    (nothing)"
	}

	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i := range lines {
		lines[i] = "    " + lines[i]
	}

	return strings.Join(lines, "\n")
}

// recallWorkspace opens a throwaway jj workspace at the project's revision,
// seeded the way a replay seeds one, and returns the function that drops it.
func recallWorkspace(ctx context.Context, root string) (string, func(), error) {
	dir := filepath.Join(scratchBase(), replayWorkspaceName(time.Now(), os.Getpid()))
	name := filepath.Base(dir)
	jj := vcs.NewJj()

	pruneKeptWorkspaces(ctx, jj, root)

	if err := jj.AddWorkspace(ctx, root, name, dir); err != nil {
		return "", nil, fmt.Errorf("recall: %w", err)
	}

	if err := seedDerivedState(root, dir); err != nil {
		dropWorkspace(ctx, jj, root, name)

		return "", nil, err
	}

	fmt.Fprintf(os.Stderr, "repeating in %s\n", dir)

	return dir, func() { dropWorkspace(ctx, jj, root, name) }, nil
}

// recallRegistry builds the project's real tool surface over the workspace.
// No local server is started, because repeating a recorded call needs every
// tool and no model.
func recallRegistry(ctx context.Context, dir string, opt options) (*tool.Registry, func(), error) {
	cfg, err := loadConfig(ctx, dir, opt.with)
	if err != nil {
		return nil, nil, err
	}

	a, err := app.New(ctx, dir, cfg, permissionGate(opt.allowAll))
	if err != nil {
		return nil, nil, fmt.Errorf("building the workspace project: %w", err)
	}

	//nolint:contextcheck // shutdown must outlive the run's context, as in headlessRun
	return a.Tools, func() {
		if cerr := a.Close(); cerr != nil {
			fmt.Fprintf(os.Stderr, "wavez: shutdown: %v\n", cerr)
		}
	}, nil
}
