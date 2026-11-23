package transcript

import (
	"cmp"
	"slices"
	"strings"
)

// finish derives what only the whole order can say.
func (b *timelineBuilder) finish() SessionTimeline {
	tl := &b.tl

	b.finishGates()
	b.finishDenials()

	for path, n := range b.reads {
		if n > 1 {
			tl.RepeatReads = append(tl.RepeatReads, FileCount{Path: path, Count: n})
		}
	}

	slices.SortFunc(tl.RepeatReads, func(x, y FileCount) int {
		return cmp.Or(cmp.Compare(y.Count, x.Count), strings.Compare(x.Path, y.Path))
	})

	for i := range tl.Turns {
		t := &tl.Turns[i]
		if !t.HasUsage {
			continue
		}

		tl.Usage.add(t.Usage)
		tl.PeakContext = max(tl.PeakContext, t.Usage.Context())
	}

	b.finishSegments()
	b.finishRuns()

	return b.tl
}

func (b *timelineBuilder) finishGates() {
	for i := range b.tl.Gates {
		round := &b.tl.Gates[i]
		step := &b.tl.Steps[round.Step]
		step.Duration = round.Duration
		step.Blocked = round.Outcome == GateBlock
		step.Label = "Stop hooks: " + string(round.Outcome)

		if round.Headline != "" {
			step.Label = clipLabel(step.Label + ": " + round.Headline)
		}
	}
}

func (b *timelineBuilder) finishDenials() {
	for i := range b.tl.Denials {
		denial := &b.tl.Denials[i]
		command := b.commands[denial.Step]

		for idx, later := range b.commands {
			if idx > denial.Step && later == command {
				denial.Retried = true
			}
		}
	}
}

func (b *timelineBuilder) finishSegments() {
	tl := &b.tl

	for i := range tl.Steps {
		step := &tl.Steps[i]
		if step.Prompt == 0 || !step.Kind.Work() {
			continue
		}

		seg := &tl.Segments[step.Prompt-1]
		seg.Steps++

		if tl.Turns[step.Turn-1].First == step.Index {
			seg.Turns++
		}
	}
}

// finishRuns measures the retrieval the session did before it edited
// anything, its longest stretch of retrieval, and the turns it took after
// its last edit.
func (b *timelineBuilder) finishRuns() {
	tl := &b.tl
	run := Run{}
	lastEditTurn := 0
	edited := false

	for i := range tl.Steps {
		step := &tl.Steps[i]

		switch step.Kind {
		case KindRetrieval:
			if run.Length == 0 {
				run.First = i
			}

			run.Length++

			if run.Length > tl.LongestRetrieval.Length {
				tl.LongestRetrieval = run
			}

			if !edited {
				tl.RetrievalBeforeEdit++
			}
		case KindProse:
		case KindEdit:
			edited, lastEditTurn, run = true, step.Turn, Run{}
		case KindShell, KindSweep, KindRefactor, KindOther, KindPrompt, KindHook, KindCompact:
			run = Run{}
		}
	}

	if edited {
		tl.TurnsAfterLastEdit = len(tl.Turns) - lastEditTurn
	}
}
