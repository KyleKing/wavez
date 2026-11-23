package hook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/session"
)

const digestLen = 8

// chainCap is how many stops in a row the handler will refuse in one turn,
// whatever they fail on.
//
// The harness has a bound of its own: after eight consecutive continuations
// caused by stop hooks it overrides the next block and ends the turn. It
// does that silently, and its count starts over whenever the agent calls a
// tool, so an agent that edits between blocks is never stopped by it. This
// cap is below the harness's so that wavez is the one that lets go, and can
// tell the person what was still failing when it did.
const chainCap = 6

// stopping decides whether the agent may stop. It runs the stop-stage
// checks on what the session changed, unless that exact change set already
// passed, could not be checked, or was already given up on, and blocks the
// stop while they fail.
func (h *Handler) stopping(ctx context.Context, store *session.Store, ev Event, rec *record) (output, error) {
	state, err := store.Update(ctx, ev.SessionID, keep)
	if err != nil {
		return output{}, wrap("loading session", err)
	}

	changed, before, err := changeSet(ctx, store, rec.Root, state)
	if err != nil {
		return output{}, err
	}

	rec.Changed = changed

	if note := h.settled(state, changed, before); note != "" {
		rec.Note = note

		return output{}, nil
	}

	verdict, err := h.runner.Run(withBaseline(ctx, state.Baseline), rec.Root, changed, gate.StageStop)
	if err != nil {
		return output{}, wrap("stop gates", err)
	}

	// A run the harness interrupted settled nothing. Recording it, as a
	// pass or as anything else, would keep the next stop from running the
	// checks this one never finished.
	if errors.Is(ctx.Err(), context.Canceled) {
		rec.Note = "interrupted, nothing recorded"

		return output{}, nil
	}

	// A check's own fixer may have rewritten files, so the change set the
	// verdict speaks for is the one on disk after the run.
	_, after, err := changeSet(ctx, store, rec.Root, state)
	if err != nil {
		return output{}, err
	}

	var res output

	// The verdict is counted before it is acted on: a block that could not
	// be recorded would repeat forever, so a failed write blocks nothing.
	_, err = store.Update(ctx, ev.SessionID, func(state *session.State) error {
		res = h.decide(state, verdict, after, ev.StopHookActive)

		return nil
	})
	if err != nil {
		return output{}, wrap("recording gate result", err)
	}

	return res, nil
}

// withBaseline tells the gates which tree the session started from, so that
// a failure that tree already had is the person's to hear about and not the
// agent's to fix. A baseline that cannot be rebuilt (outside git, before the
// first commit, or uncommitted changes too large to have been kept) is left
// out, and every failure then blocks as it always did.
func withBaseline(ctx context.Context, base session.Baseline) context.Context {
	if !base.Restorable() {
		return ctx
	}

	return gate.WithBaseline(ctx, gate.Baseline{Commit: base.Head, Tree: base.Tree})
}

func changeSet(ctx context.Context, store *session.Store, root string, state session.State) ([]string, string, error) {
	changed, err := store.Changed(ctx, state)
	if err != nil {
		return nil, "", wrap("finding changes", err)
	}

	id, err := session.Fingerprint(root, changed)
	if err != nil {
		return nil, "", wrap("fingerprinting changes", err)
	}

	return changed, id, nil
}

// settled says why a stop needs no gate run, or "" when it needs one.
func (h *Handler) settled(state session.State, changed []string, changeSet string) string {
	switch {
	case len(changed) == 0:
		return "nothing changed"
	case changeSet == state.Passed:
		return "already passed on this change set"
	case changeSet == state.Abstained:
		return "already could not be checked on this change set"
	case state.Failure.Rounds >= h.maxRounds && changeSet == state.Failure.ChangeSet:
		return "already released on this change set"
	default:
		return ""
	}
}

// decide updates the session with a verdict and picks the hook's answer.
//
// The event's stop_hook_active is true when this stop is itself a
// continuation that a stop hook caused, and false when the agent stopped of
// its own accord. It is not a reason to let a failing stop through, or the
// gates would hold for exactly one round; the round count is the guard
// against a failure that never resolves. A false value is what resets the
// chain: it means the previous turn ended, so the blocks counted so far
// belong to a turn that is over.
//
// Only a run in which every check answered and none failed is a pass. A run
// in which a check could not run or ran out of time is recorded as that, so
// the same bytes are not put through the same timeout at every later stop,
// and is never recorded as passed: the next change to the tree runs the
// checks again, and the person has been told what was not checked.
func (h *Handler) decide(state *session.State, verdict Verdict, changeSet string, continuing bool) output {
	if !continuing {
		state.Chain = 0
	}

	if !verdict.Failed {
		if verdict.Abstained {
			state.Abstain(changeSet)
		} else {
			state.Pass(changeSet)
		}

		return advise(state, verdict.Text, verdict.Notes)
	}

	text := verdict.Text
	if text == "" {
		text = "wavez: a blocking check failed without output"
	}

	failure := verdict.Fingerprint
	if failure == "" {
		failure = digest(text)
	}

	rounds := state.Fail(failure, changeSet)
	state.Chain++

	if rounds >= h.maxRounds {
		return released(fmt.Sprintf("the same way %d times in a row", rounds), text)
	}

	if state.Chain >= chainCap {
		// Mark the change set as given up on, so the next stop on the
		// same tree does not start another chain.
		state.Failure.Rounds = h.maxRounds

		return released(fmt.Sprintf("%d times in one turn", state.Chain), text)
	}

	return blocked(state, text, verdict.Notes)
}

// blocked holds the agent with text. Notes go to the person beside it, once:
// a block repeats every round and what the person was told has not changed.
func blocked(state *session.State, text, notes string) output {
	out := output{Decision: "block", Reason: clip(text)}

	if id := digest("block\x00" + notes); notes != "" && id != state.Advised {
		state.Advised = id
		out.SystemMessage = clip("wavez: " + notes)
	}

	return out
}

// released lets the agent stop and tells the user, not the model, why the
// gates are still red.
func released(how, text string) output {
	return output{released: true, SystemMessage: clip(fmt.Sprintf(
		"wavez: the gates failed %s, so the agent was allowed to stop. Still failing:\n%s", how, text,
	))}
}

// advise hands non-blocking text to the agent and notes to the person the
// first time the pair appears, and stays silent when it comes round again.
// On a Stop event additionalContext is the documented way to give the agent
// feedback that is not an error; the harness continues the turn once for
// it, which is why a pass with nothing to say sends nothing.
func advise(state *session.State, text, notes string) output {
	if text == "" && notes == "" {
		return output{}
	}

	id := digest(text + "\x00" + notes)
	if id == state.Advised {
		return output{}
	}

	state.Advised = id

	out := output{}
	if text != "" {
		out = contextFor(eventStop, text)
	}

	if notes != "" {
		out.SystemMessage = clip("wavez: " + notes)
	}

	return out
}

func digest(text string) string {
	sum := sha256.Sum256([]byte(text))

	return hex.EncodeToString(sum[:digestLen])
}
