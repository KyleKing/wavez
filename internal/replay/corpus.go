package replay

import (
	"fmt"
	"io"
	"sort"
	"strings"

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
