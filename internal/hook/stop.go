package hook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/session"
)

const digestLen = 8

// stopping decides whether the agent may stop. It runs the stop-stage
// checks on what the session changed, unless that exact change set already
// passed or was already given up on, and blocks the stop while they fail.
func (h *Handler) stopping(ctx context.Context, store *session.Store, ev event, rec *record) (output, error) {
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
	case state.Failure.Rounds >= h.maxRounds && changeSet == state.Failure.ChangeSet:
		return "already released on this change set"
	default:
		return ""
	}
}

// decide updates the session with a verdict and picks the hook's answer.
//
// The event's stop_hook_active is true when the harness is already continuing because a
// stop hook said so. It is not a reason to let a failing stop through, or
// the gates would hold for exactly one round; the round count is the guard
// against a failure that never resolves. What the flag does mark is the
// start of a turn, which bounds how long one turn can be held in total.
func (h *Handler) decide(state *session.State, verdict Verdict, changeSet string, continuing bool) output {
	if !continuing {
		state.Chain = 0
	}

	if !verdict.Failed {
		state.Pass(changeSet)

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

	if state.Chain >= h.maxRounds*chainFactor {
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
	out := output{Decision: "block", Reason: text}

	if id := digest("block\x00" + notes); notes != "" && id != state.Advised {
		state.Advised = id
		out.SystemMessage = clip("wavez: " + notes)
	}

	return out
}

// released lets the agent stop and tells the user, not the model, why the
// gates are still red.
func released(how, text string) output {
	return output{SystemMessage: clip(fmt.Sprintf(
		"wavez: the gates failed %s, so the agent was allowed to stop. Still failing:\n%s", how, text,
	))}
}

// advise hands non-blocking text to the agent and notes to the person the
// first time the pair appears, and stays silent when it comes round again.
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
