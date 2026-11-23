package report

import (
	"slices"
	"strconv"
	"strings"

	"github.com/kyleking/wavez/internal/transcript"
)

// The sizes past which something is worth a person's attention. They are
// where a short task stops looking like one, not measured optima.
const (
	// HighContext is the request size, in tokens, past which the session
	// was working near the edge of what a model holds.
	highContext = 150_000
	// LongRetrieval is how many retrieval steps in a row, or before the
	// first edit, count as surveying.
	longRetrieval = 8
	// LateTurns is how many turns after the last edit count as a tail.
	lateTurns = 4
	// NamedFiles is how many files a piece of guidance names.
	namedFiles = 3
)

// needs lists what the session left for the person, most pressing first.
// It is empty when nothing does, and the page then says nothing.
func needs(in *Input) []Item {
	tl := &in.Timeline

	var out []Item

	out = append(out, gateNeeds(tl, in.State)...)
	out = append(out, goalNeeds(in.Goal)...)
	out = append(out, testNeeds(tl)...)

	if tl.PeakContext >= highContext || tl.Compactions > 0 {
		item := Item{
			Title:  "Context ran high",
			Detail: "The largest request carried " + number(tl.PeakContext) + " tokens in",
			Copy:   "Start a new session for the next piece of work and name the files it touches.",
		}

		if tl.Compactions > 0 {
			item.Detail += ", and the conversation was compacted " + times(tl.Compactions)
		}

		item.Detail += ". Past this size a model starts losing what it read early."
		out = append(out, item)
	}

	return withIDs(out, "need")
}

// gateNeeds reads the Stop hook runs for what they told the person and
// for a session that ended with the checks still failing.
func gateNeeds(tl *transcript.SessionTimeline, state *State) []Item {
	var out []Item

	seen := map[string]bool{}
	released := false

	for i := range tl.Gates {
		round := &tl.Gates[i]

		switch round.Outcome {
		case transcript.GateReleased:
			released = true
			failing := statusNames(round.Checks, "fail")
			out = append(out, Item{
				Title: "The checks gave up on a failure",
				Detail: "The same failure came back until the agent was let go" +
					named(failing, ". Still failing: ") + ".",
				Copy: "The stop checks were still failing when you stopped" + named(failing, ": ") +
					". Fix that before anything else, and say so if you cannot.",
			})
		case transcript.GateError:
			out = append(out, Item{
				Title:  "A Stop hook could not run",
				Detail: "It abstained, so that stop was not checked: " + round.Headline,
			})
		case transcript.GatePass, transcript.GateNote, transcript.GateBlock:
		}

		out = append(out, noteNeeds(round.Checks, seen)...)
	}

	if ended := endedFailing(tl, state); ended != "" && !released {
		out = append(out, Item{
			Title:  "The session ended while checks were failing",
			Detail: ended,
			Copy: "The last stop was blocked by failing checks and the session ended there. " +
				"Pick it up and get them passing.",
		})
	}

	return out
}

// noteNeeds turns the checks a round said were not run, or were failing
// before the session, into items, once per check and status.
func noteNeeds(checks []transcript.GateCheck, seen map[string]bool) []Item {
	var out []Item

	for _, check := range checks {
		key := check.Status + " " + check.Name
		if seen[key] {
			continue
		}

		seen[key] = true

		switch check.Status {
		case "not checked":
			out = append(out, Item{
				Title:  "NOT CHECKED: " + check.Name,
				Detail: "The check could not run, so nothing verified it for this session. " + check.Line,
			})
		case "already failing":
			out = append(out, Item{
				Title:  "ALREADY FAILING: " + check.Name,
				Detail: "It failed before the session started, so it did not block. " + check.Line,
				Copy: "The " + check.Name + " check was already failing before your last session. " +
					"Fix that as its own change before building on it.",
			})
		case "not compared":
			out = append(out, Item{
				Title:  "NOT COMPARED: " + check.Name,
				Detail: "A failure could not be compared with the tree the session started from. " + check.Line,
			})
		default:
		}
	}

	return out
}

// endedFailing says why the session looks to have ended on red checks, or
// "" when it did not.
func endedFailing(tl *transcript.SessionTimeline, state *State) string {
	if n := len(tl.Gates); n > 0 && tl.Gates[n-1].Outcome == transcript.GateBlock {
		last := &tl.Gates[n-1]
		if last.Step == len(tl.Steps)-1 || !laterWork(tl, last.Step) {
			return "The last Stop hook run blocked and nothing followed it" +
				named(statusNames(last.Checks, "fail"), ". Failing: ") + "."
		}
	}

	if state != nil && state.FailingRounds > 0 {
		return "wavez's session file still counts " + plural(state.FailingRounds, "failed round") +
			" on the last change set."
	}

	return ""
}

func laterWork(tl *transcript.SessionTimeline, after int) bool {
	for i := after + 1; i < len(tl.Steps); i++ {
		if tl.Steps[i].Kind.Work() {
			return true
		}
	}

	return false
}

func goalNeeds(goal *Goal) []Item {
	if goal == nil {
		return nil
	}

	out := make([]Item, 0, len(goal.Unmet))

	for _, item := range goal.Unmet {
		out = append(out, Item{
			Title:  "Goal item not met: " + item,
			Detail: number(goal.Done) + " of " + number(goal.Total) + " items of the goal are done.",
			Copy:   "This is still not done: " + item + ". Finish it, then check with `wavez goal status`.",
		})
	}

	return out
}

func testNeeds(tl *transcript.SessionTimeline) []Item {
	out := make([]Item, 0, len(tl.TestChanges))

	for _, change := range tl.TestChanges {
		item := Item{
			Title:  "A test was " + change.What + " in " + change.Path,
			Detail: "Step " + strconv.Itoa(change.Step+1) + ": " + change.Name,
			Copy: "You " + change.What + " " + change.Name + " in " + change.Path +
				". Put it back and make it pass, or say why it cannot.",
		}
		if change.What == "skipped" {
			item.Copy = "You added a skip in " + change.Path + " (" + change.Name +
				"). Remove it and make the test pass, or say why it cannot."
		}

		out = append(out, item)
	}

	return out
}

// waste lists what the session spent that the next one need not, each with
// the sentence that would have saved it.
func waste(in *Input) []Item {
	tl := &in.Timeline

	var out []Item

	if len(tl.RepeatReads) > 0 {
		out = append(out, repeatReads(tl))
	}

	if len(tl.Sweeps) > 0 {
		out = append(out, sweepWaste(tl, in.State != nil))
	}

	if item, ok := retrievalWaste(tl); ok {
		out = append(out, item)
	}

	if tl.Edits > 0 && tl.TurnsAfterLastEdit >= lateTurns {
		out = append(out, Item{
			Title: plural(tl.TurnsAfterLastEdit, "turn") + " after the last edit",
			Detail: "Every turn re-reads the whole conversation, so turns after the work is done cost the most." +
				subagentCaveat(tl),
			Copy: "When the change is made and the checks pass, stop. " +
				"Do not re-run what already passed, and report in three lines.",
		})
	}

	out = append(out, denialWaste(tl)...)

	return withIDs(out, "waste")
}

func repeatReads(tl *transcript.SessionTimeline) Item {
	names := make([]string, 0, namedFiles)
	parts := make([]string, 0, namedFiles)
	extra := 0

	for i, file := range tl.RepeatReads {
		if i >= namedFiles {
			extra += file.Count - 1

			continue
		}

		names = append(names, file.Path)
		parts = append(parts, file.Path+" "+times(file.Count))
	}

	detail := "Read more than once: " + strings.Join(parts, ", ")
	if more := len(tl.RepeatReads) - len(names); more > 0 {
		detail += ", and " + plural(more, "other file") + " (" + plural(extra, "extra read") + ")"
	}

	return Item{
		Title:  plural(len(tl.RepeatReads), "file") + " read more than once",
		Detail: detail + ".",
		Copy:   "The work is in " + list(names) + ". Read a file once, in full, and start there.",
	}
}

func subagentCaveat(tl *transcript.SessionTimeline) string {
	if tl.Subagents == 0 {
		return ""
	}

	return " Edits made by subagents are not counted here."
}

// sweepWaste reports the whole-project runs. Wired says wavez's own
// session file exists; without it, or a Stop hook that named a check, the
// page cannot tell that anything else was checking.
func sweepWaste(tl *transcript.SessionTimeline, wired bool) Item {
	var commands []string

	steps := map[int]bool{}

	for _, sweep := range tl.Sweeps {
		steps[sweep.Step] = true

		if !slices.Contains(commands, sweep.Command) {
			commands = append(commands, sweep.Command)
		}
	}

	item := Item{
		Title:  plural(len(steps), "whole-project run") + " through the shell",
		Detail: "The agent ran " + list(commands) + " over the whole project itself.",
		Copy: "Do not run " + list(commands) + " over the whole project. " +
			"The checks for what you changed run on their own when you stop; fix what they report.",
	}

	for i := range tl.Gates {
		wired = wired || len(tl.Gates[i].Checks) > 0
	}

	if wired {
		item.Detail += " The Stop hooks already run the checks for what changed."
	} else {
		item.Detail += " Nothing in this session shows wavez's checks running on stop. This wires them in:"
		item.Copy = "wavez init"
	}

	return item
}

func retrievalWaste(tl *transcript.SessionTimeline) (Item, bool) {
	place := "The change is in " + list(firstFiles(tl.Edited)) + ". "
	if len(tl.Edited) == 0 {
		place = ""
	}

	say := place + "Make the change without surveying the repository first."

	switch {
	case tl.Edits > 0 && tl.RetrievalBeforeEdit >= longRetrieval:
		return Item{
			Title: plural(tl.RetrievalBeforeEdit, "retrieval step") + " before the first edit",
			Detail: "The agent looked around this long before changing anything. " +
				"A prompt that names the file skips it.",
			Copy: say,
		}, true
	case tl.LongestRetrieval.Length >= longRetrieval:
		return Item{
			Title: plural(tl.LongestRetrieval.Length, "retrieval step") + " in a row",
			Detail: "Starting at step " + strconv.Itoa(tl.LongestRetrieval.First+1) +
				", the agent read and searched without changing or running anything.",
			Copy: say,
		}, true
	default:
		return Item{}, false
	}
}

func denialWaste(tl *transcript.SessionTimeline) []Item {
	out := make([]Item, 0, len(tl.Denials))

	for _, denial := range tl.Denials {
		item := Item{
			Title: "A rename by text was denied",
			Detail: "Step " + strconv.Itoa(denial.Step+1) +
				" rewrote a Go name with a text tool and the hook refused it once.",
			Copy: "Rename Go symbols with `wavez rename <file>:<Name> <NewName>`, never with sed or perl.",
		}

		if denial.Retried {
			item.Title += " and sent again"
			item.Detail += " The agent sent the same command again instead of using the rename command."
		}

		if len(denial.Suggested) > 0 {
			item.Copy = denial.Suggested[0]
		}

		out = append(out, item)
	}

	return out
}

func firstFiles(files []string) []string {
	if len(files) > namedFiles {
		return files[:namedFiles]
	}

	return files
}

// statusNames lists the checks with one status, each once.
func statusNames(checks []transcript.GateCheck, status string) []string {
	var out []string

	for _, c := range checks {
		if c.Status == status && !slices.Contains(out, c.Name) {
			out = append(out, c.Name)
		}
	}

	return out
}

// named joins names after a lead-in, and says nothing for no names.
func named(names []string, lead string) string {
	if len(names) == 0 {
		return ""
	}

	return lead + list(names)
}

func times(n int) string {
	if n == 1 {
		return "once"
	}

	return number(n) + " times"
}

// withIDs gives each item the id its copy button points at.
func withIDs(items []Item, prefix string) []Item {
	for i := range items {
		items[i].ID = prefix + "-" + strconv.Itoa(i+1)
	}

	return items
}
