package agent

import (
	"context"
	"fmt"

	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/tool"
)

// DefaultMaxReviewRounds bounds how many times one run may be reviewed. Two
// is the whole design: an objection buys the model one more attempt, and the
// second verdict is recorded rather than acted on.
const DefaultMaxReviewRounds = 2

// ReviewResult is a reviewer's answer to whether a run's diff does what the
// task asked. The zero value means no review happened.
type ReviewResult string

// Answers a review may return.
const (
	// ReviewOK means the reviewer read the diff and it does what was asked.
	ReviewOK ReviewResult = "ok"
	// ReviewObjection means the reviewer read the diff and it does not.
	ReviewObjection ReviewResult = "objection"
	// ReviewSkipped means no review happened: the diff was too large to read,
	// the diff could not be produced, or the reviewer returned no verdict. It
	// is never a claim about the change, which is why it never reads as a
	// pass.
	ReviewSkipped ReviewResult = "skipped"
)

// Verdict is one review's answer.
type Verdict struct {
	Result ReviewResult
	// Note carries the objection when Result is ReviewObjection and why no
	// review happened when it is ReviewSkipped.
	Note string
}

// Review is what a Reviewer is given: the task as the user stated it, and the
// run that claims to have done it.
type Review struct {
	Task string
	// Checkpoint is the run's starting operation id, so a reviewer can diff
	// the whole run rather than the last turn.
	Checkpoint string
	Changes    []tool.Change
}

// Reviewer judges whether a run's diff does what its task asked, once the
// deterministic gates have already passed. It is the judgment half of
// DESIGN.md's thesis and deliberately not a gate: a Reviewer never fails a
// run, so it returns a Verdict rather than an error, and reports its own
// failure as ReviewSkipped.
type Reviewer interface {
	Review(ctx context.Context, rv Review) Verdict
}

// WithReviewer configures Run to have a model read the run's diff against the
// task once the gates pass. An objection is appended as a new turn so the
// model gets one attempt to fix it; an objection on the next attempt is
// carried on Outcome and in the thread log while the run still completes.
func WithReviewer(rv Reviewer) Option { return func(o *Options) { o.Reviewer = rv } }

// WithMaxReviewRounds overrides DefaultMaxReviewRounds.
func WithMaxReviewRounds(n int) Option { return func(o *Options) { o.MaxReviewRounds = n } }

// reviewOrComplete runs once the gates have passed. A review that objects
// hands the objection to the model as a new turn, once. A run whose second
// review still objects completes carrying the objection, because a reviewer's
// disagreement is judgment and the user is the one who settles it.
//
// A run that called an edit tool and left no net Change follows the same
// escalate-then-stop rule, since completing would report the model's belief
// that it acted rather than whether it did.
func (r *run) reviewOrComplete(ctx context.Context) (bool, Outcome, error) {
	if len(r.changes) == 0 && r.editAttempted {
		return r.handleTalkedNotActed(ctx, StopStagnant,
			"the model called an edit tool but left no file changed again after escalating",
			"That turn ended with no files changed even though an edit tool was called. Read "+
				"the last tool result, fix whatever made the edit not apply, and make the edit "+
				"before reporting anything finished.")
	}

	if r.loop.options.Reviewer == nil || len(r.changes) == 0 {
		return r.complete(ctx)
	}

	verdict := r.loop.options.Reviewer.Review(ctx, Review{
		Task:       r.task,
		Checkpoint: r.outcome.Checkpoint,
		Changes:    r.changes,
	})
	r.reviewRounds++
	r.outcome.Review = verdict

	if err := r.logReview(verdict); err != nil {
		return true, Outcome{}, err
	}

	if verdict.Result != ReviewObjection || r.reviewRounds >= r.loop.options.MaxReviewRounds {
		return r.complete(ctx)
	}

	if err := r.thread.AppendUser(ctx, reviewCritique(verdict.Note)); err != nil {
		return true, Outcome{}, fmt.Errorf("appending review objection: %w", err)
	}

	return false, Outcome{}, nil
}

func reviewCritique(objection string) string {
	return "A review of your diff against the task objects: " + objection +
		"\nEither change the diff so it does what the task asked, or say in one sentence why the " +
		"objection is wrong. Do not restate what you already did."
}

func (r *run) logReview(v Verdict) error {
	detail := map[string]any{"round": r.reviewRounds, "result": string(v.Result)}
	if v.Note != "" {
		detail["note"] = v.Note
	}

	ev := event.Event{Kind: event.KindReview, Text: reviewText(v), Detail: detail}
	if _, err := r.thread.Log().Append(ev); err != nil {
		return fmt.Errorf("logging review round: %w", err)
	}

	return nil
}

func reviewText(v Verdict) string {
	switch v.Result {
	case ReviewObjection:
		return "review objects: " + v.Note
	case ReviewSkipped:
		return "not reviewed: " + v.Note
	case ReviewOK:
		return "review: the diff does what the task asked"
	default:
		return "review: " + string(v.Result)
	}
}
