package replay

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/kyleking/wavez/internal/bench"
)

// Corpus writes the rates one run cannot show. Every number in the
// roadmap's tool-efficiency item came from an ad-hoc script over the
// records file, which left the dogfood loop's own evidence unreachable from
// the tool the loop is about.
//
// It reports what happened rather than what it means: a tool's error rate
// counts refusals separately, because a refusal that worked is not a
// failure, and the same distinction turned `delete`'s 60% into a third.
func Corpus(recs []Record, w io.Writer) error {
	if len(recs) == 0 {
		if _, err := io.WriteString(w, "no runs recorded\n"); err != nil {
			return fmt.Errorf("writing corpus report: %w", err)
		}

		return nil
	}

	var b strings.Builder

	writeRuns(&b, recs)
	writeTasks(&b, recs)
	writeTools(&b, recs)
	writeTurns(&b, recs)
	writeGates(&b, recs)
	writeFinish(&b, recs)

	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("writing corpus report: %w", err)
	}

	return nil
}

func writeRuns(b *strings.Builder, recs []Record) {
	var complete, checked, checksHeld, checksTotal int

	for i := range recs {
		if recs[i].Complete {
			complete++
		}

		if len(recs[i].Checks) == 0 {
			continue
		}

		checked++

		for _, c := range recs[i].Checks {
			checksTotal++

			if c.Pass {
				checksHeld++
			}
		}
	}

	fmt.Fprintf(b, "%d runs, %s ended complete\n", len(recs), share(complete, len(recs)))
	fmt.Fprintf(b, "%d runs assert checks, %s of %d checks held\n",
		checked, share(checksHeld, checksTotal), checksTotal)
}

func writeTasks(b *strings.Builder, recs []Record) {
	type tally struct {
		runs, done, turns int
	}

	byTask := map[string]*tally{}

	for i := range recs {
		t, ok := byTask[recs[i].Task]
		if !ok {
			t = &tally{}
			byTask[recs[i].Task] = t
		}

		t.runs++
		t.turns += recs[i].Stats.Turns

		if allChecksHeld(recs[i]) {
			t.done++
		}
	}

	b.WriteString("\ntask     runs   did the work   mean turns\n")

	for _, name := range sortedKeys(byTask) {
		t := byTask[name]
		fmt.Fprintf(b, "%-8s %4d   %-12s   %10.1f\n",
			name, t.runs, share(t.done, t.runs), float64(t.turns)/float64(t.runs))
	}
}

// allChecksHeld is the honest completion signal: the loop's own `complete`
// says a run ended tidily and says nothing about whether it did the task.
func allChecksHeld(r Record) bool {
	if len(r.Checks) == 0 {
		return false
	}

	for _, c := range r.Checks {
		if !c.Pass {
			return false
		}
	}

	return true
}

func writeTools(b *strings.Builder, recs []Record) {
	totals := map[string]*bench.ToolStat{}

	for i := range recs {
		for _, t := range recs[i].Stats.Tools {
			sum, ok := totals[t.Name]
			if !ok {
				sum = &bench.ToolStat{Name: t.Name, Causes: map[string]int{}}
				totals[t.Name] = sum
			}

			sum.Calls += t.Calls
			sum.Errors += t.Errors
			sum.Refusals += t.Refusals
			sum.ResultBytes += t.ResultBytes

			for cause, n := range t.Causes {
				sum.Causes[cause] += n
			}
		}
	}

	b.WriteString("\ntool           calls   failed            refused   causes\n")

	for _, name := range sortedToolKeys(totals) {
		t := totals[name]
		failed := t.Errors - t.Refusals
		fmt.Fprintf(b, "%-14s %5d   %-15s   %7d   %s\n",
			name, t.Calls, share(failed, t.Calls), t.Refusals, causeList(t.Causes, t.Errors))
	}
}

// causeList is the error causes most common first, which is what says
// whether one rate is one problem or several. It closes with the errors no
// cause covers, because the taxonomy reached the call sites gradually and a
// report that hid that would read as a complete breakdown of an
// incomplete one.
func causeList(causes map[string]int, errors int) string {
	if len(causes) == 0 {
		if errors == 0 {
			return "-"
		}

		return fmt.Sprintf("unclassified %d", errors)
	}

	names := make([]string, 0, len(causes))
	for name := range causes {
		names = append(names, name)
	}

	sort.Slice(names, func(i, j int) bool {
		if causes[names[i]] != causes[names[j]] {
			return causes[names[i]] > causes[names[j]]
		}

		return names[i] < names[j]
	})

	parts := make([]string, 0, len(names))
	for _, n := range names {
		parts = append(parts, fmt.Sprintf("%s %d", n, causes[n]))
	}

	if rest := errors - total(causes); rest > 0 {
		parts = append(parts, fmt.Sprintf("unclassified %d", rest))
	}

	return strings.Join(parts, ", ")
}

func total(causes map[string]int) int {
	sum := 0
	for _, n := range causes {
		sum += n
	}

	return sum
}

const asPercent = 100

func share(part, whole int) string {
	if whole == 0 {
		return "-"
	}

	return fmt.Sprintf("%d/%d (%.0f%%)", part, whole, asPercent*float64(part)/float64(whole))
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	sort.Strings(out)

	return out
}

func sortedToolKeys(m map[string]*bench.ToolStat) []string {
	out := sortedKeys(m)
	sort.SliceStable(out, func(i, j int) bool { return m[out[i]].Calls > m[out[j]].Calls })

	return out
}

// writeTurns is where the corpus's turns went. Harness is the number this
// project is trying to move: a turn spent reacting to a tool error or to
// gate feedback is one the harness cost the run rather than one the task
// needed.
func writeTurns(b *strings.Builder, recs []Record) {
	var a bench.Attribution

	for i := range recs {
		t := recs[i].Stats.TurnsBy
		a.Productive += t.Productive
		a.Retrieval += t.Retrieval
		a.Harness += t.Harness
		a.Prose += t.Prose
	}

	total := a.Total()
	if total == 0 {
		return
	}

	fmt.Fprintf(b, "\n%d turns attributed: productive %s, retrieval %s, harness %s, prose %s\n",
		total, share(a.Productive, total), share(a.Retrieval, total),
		share(a.Harness, total), share(a.Prose, total))
	b.WriteString("harness is an estimate; the other three are exact from the log\n")
}

// writeGates reports whether the gates are getting quieter or wronger. A
// false alarm is a gate passing over the same change set it just failed
// over, so nothing about the code moved between the two answers.
func writeGates(b *strings.Builder, recs []Record) {
	var rounds, failures, falseAlarms int

	for i := range recs {
		rounds += recs[i].Stats.GateRounds
		failures += recs[i].Stats.GateFailures
		falseAlarms += recs[i].Stats.GateFalseAlarms
	}

	if rounds == 0 {
		return
	}

	fmt.Fprintf(b, "\n%d gate rounds, %s failed, %d retracted a failure over an unchanged tree\n",
		rounds, share(failures, rounds), falseAlarms)
}

// writeFinish counts the runs that completed having done something of the
// wrong shape. Every gate passed on each of these, which is what the
// deterministic finish checks exist to catch and what no other number here
// can show.
func writeFinish(b *strings.Builder, recs []Record) {
	counts := map[string]int{}
	runs := 0

	for i := range recs {
		if len(recs[i].Stats.FinishFindings) == 0 {
			continue
		}

		runs++

		for check, n := range recs[i].Stats.FinishFindings {
			counts[check] += n
		}
	}

	if runs == 0 {
		return
	}

	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}

	sort.Slice(names, func(i, j int) bool {
		if counts[names[i]] != counts[names[j]] {
			return counts[names[i]] > counts[names[j]]
		}

		return names[i] < names[j]
	})

	fmt.Fprintf(b, "\n%s of runs finished with a finding, every gate having passed\n",
		share(runs, len(recs)))

	for _, n := range names {
		fmt.Fprintf(b, "  %-32s %4d\n", n, counts[n])
	}
}

// Since keeps the runs recorded on or after from. The corpus spans every
// harness this project has had, so a rate over all of it averages tools
// that no longer behave the way they did: the `str_replace` failures
// recorded before its schema stated a top-level oneOf are counting a hole
// that a later run cannot fall into.
func Since(recs []Record, from time.Time) []Record {
	out := make([]Record, 0, len(recs))

	for i := range recs {
		// A record whose timestamp does not parse is kept: dropping it
		// would silently narrow the corpus for a reason that has nothing
		// to do with what the caller asked for.
		at, err := time.Parse(time.RFC3339, recs[i].Started)
		if err != nil || !at.Before(from) {
			out = append(out, recs[i])
		}
	}

	return out
}
