package hook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

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

// stopping decides whether the agent may stop. The gates go first, and a
// stop they hold or let go of is answered as it is. A stop they have no
// objection to is put to the project's goal, and whatever is left to say
// goes to the person as one line.
func (h *Handler) stopping(ctx context.Context, store *session.Store, ev Event, rec *record) (output, error) {
	started := time.Now()

	res, status, err := h.gated(ctx, store, ev, rec)
	if err != nil || res.Decision != "" || res.released || status == statusUnsettled {
		return res, err
	}

	goalCtx, cancel := context.WithDeadline(ctx, started.Add(stopWindow))
	defer cancel()

	person, agent, hold, goalErr := h.goals(goalCtx, rec.Root)
	if goalErr != nil {
		person, agent, hold = "goal not checked: "+oneLine(goalErr.Error()), "", false
	}

	rec.Goal = person
	rec.Review = reviewed(ctx, store, ev.SessionID, rec.Changed)

	_, err = store.Update(ctx, ev.SessionID, func(state *session.State) error {
		// A stop the gates ran on has already had its chain reset.
		if !ev.StopHookActive && status != statusPassed && status != statusAbstained {
			state.Chain = 0
		}

		sum := summary{changed: len(rec.Changed), status: status, goal: person, review: rec.Review}
		res = h.close(state, res, sum, agent, hold)

		return nil
	})
	if err != nil {
		return output{}, wrap("recording the end of the turn", err)
	}

	return res, nil
}

// What the gates made of a stop, as the end-of-turn line words it.
const (
	// StatusUnsettled is a run the harness interrupted, which says nothing.
	statusUnsettled = "unsettled"
	// StatusUnchanged is a session that changed nothing.
	statusUnchanged = ""
	statusPassed    = "checks passed"
	statusAbstained = "checks could not run"
	statusFailing   = "checks still failing"
	// The two below are the same verdicts met again on an unchanged tree.
	statusPassedBefore    = "passed before"
	statusAbstainedBefore = "abstained before"
)

// said is a status in the words the person reads. A verdict met again reads
// as it did the first time, so that the same line is not shown twice.
func said(status string) string {
	switch status {
	case statusPassedBefore:
		return statusPassed
	case statusAbstainedBefore:
		return statusAbstained
	default:
		return status
	}
}

// stopWindow is how long after a Stop event arrives the goal may still be
// running. It ends inside the timeout init gives the Stop hook, so the
// harness never has to kill a goal check.
const stopWindow = gate.DefaultStopBudget + stopMargin - 10*time.Second

// gated runs the stop-stage checks on what the session changed, unless that
// exact change set already passed, could not be checked, or was already
// given up on, and blocks the stop while they fail. Its second result is
// what the checks made of the change set.
func (h *Handler) gated(ctx context.Context, store *session.Store, ev Event, rec *record) (output, string, error) {
	state, err := store.Update(ctx, ev.SessionID, keep)
	if err != nil {
		return output{}, "", wrap("loading session", err)
	}

	changed, before, err := changeSet(ctx, store, rec.Root, state)
	if err != nil {
		return output{}, "", err
	}

	rec.Changed = changed

	if note, status := h.settled(state, changed, before); note != "" {
		rec.Note = note

		return output{}, status, nil
	}

	verdict, err := h.runner.Run(withBaseline(ctx, state.Baseline), rec.Root, changed, gate.StageStop)
	if err != nil {
		return output{}, "", wrap("stop gates", err)
	}

	// A run the harness interrupted settled nothing. Recording it, as a
	// pass or as anything else, would keep the next stop from running the
	// checks this one never finished.
	if errors.Is(ctx.Err(), context.Canceled) {
		rec.Note = "interrupted, nothing recorded"

		return output{}, statusUnsettled, nil
	}

	// A check's own fixer may have rewritten files, so the change set the
	// verdict speaks for is the one on disk after the run.
	_, after, err := changeSet(ctx, store, rec.Root, state)
	if err != nil {
		return output{}, "", err
	}

	var res output

	// The verdict is counted before it is acted on: a block that could not
	// be recorded would repeat forever, so a failed write blocks nothing.
	_, err = store.Update(ctx, ev.SessionID, func(state *session.State) error {
		res = h.decide(state, verdict, after, ev.StopHookActive)

		return nil
	})
	if err != nil {
		return output{}, "", wrap("recording gate result", err)
	}

	status := statusPassed
	if verdict.Abstained {
		status = statusAbstained
	}

	return res, status, nil
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

// settled says why a stop needs no gate run and what the earlier run made of
// the change set, or "" when it needs one.
func (h *Handler) settled(state session.State, changed []string, changeSet string) (string, string) {
	switch {
	case len(changed) == 0:
		return "nothing changed", statusUnchanged
	case changeSet == state.Passed:
		return "already passed on this change set", statusPassedBefore
	case changeSet == state.Abstained:
		return "already could not be checked on this change set", statusAbstainedBefore
	case state.Failure.Rounds >= h.maxRounds && changeSet == state.Failure.ChangeSet:
		return "already released on this change set", statusFailing
	default:
		return "", ""
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

// summary is what the end-of-turn line is built from.
type summary struct {
	// status is what the gates made of the change set.
	status string
	// goal is the goal's one line, and empty when the project has none.
	goal string
	// review names the tests the session made ask for less.
	review  string
	changed int
}

// line is the one line a person reads when a turn ends, and is empty when
// the turn changed nothing and the project has no goal.
func (s summary) line() string {
	var parts []string

	// The person heard about a failure the gates gave up on when they did.
	if s.status == statusFailing && s.goal == "" && s.review == "" {
		return ""
	}

	if s.changed > 0 {
		noun := "files"
		if s.changed == 1 {
			noun = "file"
		}

		parts = append(parts, fmt.Sprintf("%d %s changed", s.changed, noun), said(s.status))
	}

	if s.goal != "" {
		parts = append(parts, s.goal)
	}

	if s.review != "" {
		parts = append(parts, s.review)
	}

	return strings.Join(parts, " · ")
}

// close ends a stop the gates did not object to. An unmet goal that asks to
// hold the turn holds it, under the same two bounds the gates keep: the same
// unmet conditions maxRounds times in a row, or chainCap blocks in one turn.
// Otherwise the person gets the end-of-turn line, once per distinct line, in
// front of whatever the gates had for them.
func (h *Handler) close(state *session.State, res output, sum summary, agent string, hold bool) output {
	if !hold || agent == "" {
		state.Goal = session.Failure{}

		return described(state, res, sum.line())
	}

	if id := digest(agent); state.Goal.Fingerprint != id {
		state.Goal = session.Failure{Fingerprint: id}
	}

	state.Goal.Rounds++
	state.Chain++

	if state.Goal.Rounds >= h.maxRounds || state.Chain >= chainCap {
		return described(state, res, sum.line()+" · the agent was allowed to stop")
	}

	out := output{Decision: "block", Reason: agent}
	if res.Specific != nil {
		out.Reason = clip(agent + "\n" + res.Specific.AdditionalContext)
	}

	return described(state, out, sum.line())
}

// described puts line in front of the output's message for the person,
// unless it is the line they were last shown.
func described(state *session.State, res output, line string) output {
	id := digest(line)
	if line == "" || id == state.Summary {
		return res
	}

	state.Summary = id

	notes := strings.TrimPrefix(res.SystemMessage, "wavez: ")
	res.SystemMessage = "wavez: " + line

	if notes != "" {
		res.SystemMessage = clip(res.SystemMessage + "\n" + notes)
	}

	return res
}

// reviewed names the tests the session made ask for less, for the person.
// A session whose starting tree was not kept, or a diff git cannot make,
// has nothing to compare and says nothing.
func reviewed(ctx context.Context, store *session.Store, id string, changed []string) string {
	var tests []string

	for _, path := range changed {
		if strings.HasSuffix(path, "_test.go") {
			tests = append(tests, path)
		}
	}

	state, ok := store.Read(id)
	if !ok || len(tests) == 0 {
		return ""
	}

	diff, err := store.Diff(ctx, state.Baseline, tests)
	if err != nil {
		return ""
	}

	return review(weakened(diff))
}

// oneLine folds text onto a single line.
func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func digest(text string) string {
	sum := sha256.Sum256([]byte(text))

	return hex.EncodeToString(sum[:digestLen])
}
