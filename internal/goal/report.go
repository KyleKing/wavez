package goal

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const (
	// The personLimit bounds the one line a person is shown at the end of a
	// turn.
	personLimit = 200
	// AgentLimit is the most bytes StopReport ever hands the agent. The
	// agent pays for that text again on every later turn of its session.
	AgentLimit = 600
	// The sentenceLimit bounds how much of the goal's sentence the agent is
	// handed back.
	sentenceLimit = 120
)

// Summary is a goal's progress in the numbers a status line shows.
type Summary struct {
	// Goal is the goal's sentence.
	Goal string `json:"goal"`
	// Unmet names the conditions that are not done, in file order.
	Unmet []string `json:"unmet"`
	// Total counts every condition, and Done, Unknown, and CouldNotCheck
	// the ones with that status. What is left is len(Unmet).
	Total         int `json:"total"`
	Done          int `json:"done"`
	Unknown       int `json:"unknown"`
	CouldNotCheck int `json:"could_not_check"`
	// Stale reports that a test or command condition has no verdict for
	// the condition as it is written now, so Done undercounts until the
	// next full evaluation.
	Stale bool `json:"stale"`
}

// Summary counts the report's statuses.
func (r Report) Summary() Summary {
	summary := Summary{Goal: r.Goal, Total: len(r.Results), Unmet: []string{}}

	for _, result := range r.Results {
		switch result.Status {
		case StatusDone:
			summary.Done++
		case StatusNotDone:
			summary.Unmet = append(summary.Unmet, result.Name)
		case StatusCouldNotCheck:
			summary.CouldNotCheck++
		case StatusUnknown:
			summary.Unknown++
		}
	}

	summary.Stale = summary.Unknown > 0

	return summary
}

// Snapshot is the goal's progress as far as it can be told without starting
// a process: the file, grep, and absent conditions are checked now, and a
// test or command condition reports the verdict its last full evaluation
// cached. That makes it cheap enough for a status line. It cannot see that
// the tree changed since that evaluation, which Evaluate can, so it shows a
// test's last verdict until the end of the turn runs it again. A project
// with no goal gets ErrNoGoal.
func Snapshot(root string) (Summary, error) {
	goal, err := Load(root)
	if err != nil {
		return Summary{}, err
	}

	files := openReader(root)
	defer files.close()

	cached := readState(root)
	report := Report{Goal: goal.Sentence, Block: goal.Block}

	for index := range goal.Items {
		item := goal.Items[index]
		result := files.check(item)

		if last, ok := cached.cachedFor(item); ok && item.Kind.Expensive() {
			result.Status, result.Detail = last.Verdict, last.Detail
		}

		report.Results = append(report.Results, result)
	}

	return report.Summary(), nil
}

// Render is the report for a person: the sentence, one line per condition,
// and the count.
func (r Report) Render() string {
	var b strings.Builder

	b.WriteString(r.Goal + "\n")

	for _, result := range r.Results {
		switch result.Status {
		case StatusDone:
			fmt.Fprintf(&b, "[x] %s\n", result.Name)
		case StatusNotDone:
			fmt.Fprintf(&b, "[ ] %s — %s\n", result.Name, result.Detail)
		case StatusCouldNotCheck:
			fmt.Fprintf(&b, "[?] %s — could not check: %s\n", result.Name, result.Detail)
		case StatusUnknown:
			fmt.Fprintf(&b, "[~] %s — not run yet: %s\n", result.Name, result.Detail)
		}
	}

	b.WriteString(r.Summary().Tally() + "\n")

	return b.String()
}

// Tally is the count as one phrase, such as `2 of 4 done, 1 could not be
// checked`. A condition that could not be checked is named apart from the
// ones that are not done, because it is not a failure.
func (s Summary) Tally() string {
	if s.Total == 0 {
		return "no conditions yet: add one with `wavez goal add`"
	}

	tally := fmt.Sprintf("%d of %d done", s.Done, s.Total)

	if s.CouldNotCheck > 0 {
		tally += fmt.Sprintf(", %d could not be checked", s.CouldNotCheck)
	}

	if s.Unknown > 0 {
		tally += fmt.Sprintf(", %d not run yet", s.Unknown)
	}

	return tally
}

// StopReport evaluates root's goal in full for the end of a turn. It
// returns one short line for the person, the text for the agent, and
// whether the turn should be held.
//
// The agent's text is empty unless the goal sets block and a condition is
// not done. It then names only the unmet conditions, each with its one line
// of detail, and never exceeds AgentLimit bytes. A condition that could not
// be checked is in the person's line only: it never blocks and the agent
// never hears of it. A project with no goal gets three zero values. An
// error is a goal file that could not be read or a cache that could not be
// written, and is never a reason to hold a turn.
func StopReport(ctx context.Context, root string) (string, string, bool, error) {
	goal, err := Load(root)
	if errors.Is(err, ErrNoGoal) {
		return "", "", false, nil
	}

	if err != nil {
		return "", "", false, err
	}

	report, err := Evaluate(ctx, root, goal, Options{Expensive: true})
	person := report.personLine()

	if !goal.Block || len(report.Summary().Unmet) == 0 || ctx.Err() != nil {
		return person, "", false, err
	}

	return person, report.agentText(), true, err
}

// personLine is the report as one bounded line.
func (r Report) personLine() string {
	summary := r.Summary()
	line := fmt.Sprintf("goal %d/%d", summary.Done, summary.Total)

	if summary.Total == 0 {
		return line + ": no conditions yet"
	}

	if summary.Done == summary.Total {
		return line + " done"
	}

	var parts []string

	for _, status := range []Status{StatusNotDone, StatusCouldNotCheck, StatusUnknown} {
		var names []string

		for _, result := range r.Results {
			if result.Status == status {
				names = append(names, result.Name)
			}
		}

		if len(names) > 0 {
			parts = append(parts, string(status)+": "+strings.Join(names, ", "))
		}
	}

	return clip(line+": "+strings.Join(parts, "; "), personLimit)
}

// agentText lists the unmet conditions under one sentence, within
// AgentLimit. Conditions that do not fit are counted, not listed.
func (r Report) agentText() string {
	var b strings.Builder

	b.WriteString("Goal not done: " + clip(r.Goal, sentenceLimit) + "\n")

	// The longest line that says how many conditions were left out.
	const reserve = len("- and 999999 more\n")

	left := 0

	for _, result := range r.Results {
		if result.Status != StatusNotDone {
			continue
		}

		line := "- " + clip(result.Name+": "+result.Detail, detailLimit) + "\n"
		if left > 0 || b.Len()+len(line)+reserve > AgentLimit {
			left++

			continue
		}

		b.WriteString(line)
	}

	if left > 0 {
		fmt.Fprintf(&b, "- and %d more\n", left)
	}

	return b.String()
}
