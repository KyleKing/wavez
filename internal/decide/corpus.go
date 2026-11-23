package decide

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"
)

// ErrNoWant is returned for a corpus line that carries no label.
var ErrNoWant = errors.New("want is empty")

// Case is one labeled line of a corpus: a state and the choice key a
// person says is right for it.
type Case struct {
	State string `json:"state"`
	Want  string `json:"want"`
}

// ReadCorpus reads a JSONL corpus, one Case per line. Blank lines are
// skipped and keys other than state and want are ignored, so a line of the
// decision log becomes a case by adding its want. A line that is not a JSON
// object, or that has no want, fails with its line number.
func ReadCorpus(r io.Reader) ([]Case, error) {
	var cases []Case

	reader := bufio.NewReader(r)

	for number := 1; ; number++ {
		line, err := reader.ReadBytes('\n')
		last := errors.Is(err, io.EOF)

		if err != nil && !last {
			return nil, fmt.Errorf("reading corpus line %d: %w", number, err)
		}

		if text := bytes.TrimSpace(line); len(text) > 0 {
			var c Case
			if jsonErr := json.Unmarshal(text, &c); jsonErr != nil {
				return nil, fmt.Errorf("corpus line %d: %w", number, jsonErr)
			}

			if c.Want == "" {
				return nil, fmt.Errorf("corpus line %d: %w", number, ErrNoWant)
			}

			cases = append(cases, c)
		}

		if last {
			return cases, nil
		}
	}
}

// Tally is how many cases something answered and how many of those matched
// their label.
type Tally struct {
	Answered int
	Correct  int
}

// ModelTally is how one model backend did on the cases the rules left.
type ModelTally struct {
	Backend string
	Model   string
	Tally
}

// Miss is one case that was decided wrongly.
type Miss struct {
	Want string
	Got  string
	// Backend is who decided it: BackendRule, BackendFallback, or a
	// model backend's name.
	Backend string
	// State is the start of the case's state on one line.
	State string
}

// Report is the result of running a corpus through an Engine. It keeps
// the rules apart from what came after them, because the two answer
// different questions: whether a rule should exist, and whether a model is
// worth asking about what the rules leave.
type Report struct {
	// Reasons counts why cases fell back, by Decision.Reason.
	Reasons map[string]int
	// Question and Fallback are the question's name and fallback key.
	Question string
	Fallback string
	// Models has one entry per model backend that answered, by name.
	Models []ModelTally
	// Wrong lists the missed cases in corpus order.
	Wrong []Miss
	// Rules is what the rules answered. Its Answered over Total is their
	// coverage, and its Correct over its Answered is their precision.
	Rules Tally
	// FellBack is the cases neither a rule nor a model answered. Its
	// Correct is how many of them were labeled with the fallback.
	FellBack Tally
	// Total is how many cases were decided.
	Total int
	// LeftForFallback is how many of the cases the rules left were labeled
	// with the fallback: what never asking a model would have scored.
	LeftForFallback int
	// Skipped is how many cases were left when the context ended.
	Skipped int
}

// Left is how many cases the rules did not answer.
func (r Report) Left() int {
	return r.Total - r.Rules.Answered
}

// Evaluate decides every case with d, at most parallel at a time, and
// compares each choice with the case's label. A case labeled with a key
// outside q's choices counts as wrong. Evaluate stops starting cases when
// ctx ends and reports the rest as skipped.
func Evaluate(ctx context.Context, d *Engine, q Question, rules []Rule, cases []Case, parallel int) Report {
	decisions := make([]*Decision, len(cases))

	var wg sync.WaitGroup

	slots := make(chan struct{}, max(parallel, 1))

	for i, c := range cases {
		slots <- struct{}{}

		if ctx.Err() != nil {
			<-slots

			break
		}

		wg.Go(func() {
			defer func() { <-slots }()

			got := d.Decide(ctx, q, c.State, rules...)
			decisions[i] = &got
		})
	}

	wg.Wait()

	report := Report{Question: q.Name, Fallback: q.Fallback, Reasons: map[string]int{}}
	models := map[string]*ModelTally{}

	for i, got := range decisions {
		if got == nil {
			report.Skipped++

			continue
		}

		report.count(models, *got, cases[i])
	}

	for _, tally := range models {
		report.Models = append(report.Models, *tally)
	}

	slices.SortFunc(report.Models, func(a, b ModelTally) int {
		return strings.Compare(a.Backend+" "+a.Model, b.Backend+" "+b.Model)
	})

	return report
}

// count adds one decided case to the report.
func (r *Report) count(models map[string]*ModelTally, got Decision, c Case) {
	r.Total++

	tally := &r.Rules

	if got.Backend != BackendRule {
		if c.Want == r.Fallback {
			r.LeftForFallback++
		}

		switch got.Backend {
		case BackendFallback:
			tally = &r.FellBack
			r.Reasons[got.Reason]++
		default:
			name := got.Backend + " " + got.Model
			if models[name] == nil {
				models[name] = &ModelTally{Backend: got.Backend, Model: got.Model}
			}

			tally = &models[name].Tally
		}
	}

	tally.Answered++

	if got.Choice == c.Want {
		tally.Correct++

		return
	}

	r.Wrong = append(r.Wrong, Miss{Want: c.Want, Got: got.Choice, Backend: got.Backend, State: excerpt(c.State)})
}

// Render prints the report as plain text: the rules' coverage and
// precision first, then what became of the cases they left, then each
// wrong case under who decided it.
func (r Report) Render() string {
	var b strings.Builder

	fmt.Fprintf(&b, "question: %s\ncases: %d\n", r.Question, r.Total)

	if r.Skipped > 0 {
		fmt.Fprintf(&b, "skipped: %d (stopped early)\n", r.Skipped)
	}

	fmt.Fprintf(&b, "\nrules: answered %d of %d (coverage %s); %d right, %d wrong (precision %s)\n",
		r.Rules.Answered, r.Total, percent(r.Rules.Answered, r.Total),
		r.Rules.Correct, r.Rules.Answered-r.Rules.Correct, percent(r.Rules.Correct, r.Rules.Answered))

	left := r.Left()
	right := r.Rules.Correct + r.FellBack.Correct

	fmt.Fprintf(&b, "\nthe %d cases the rules left:\n", left)

	for _, m := range r.Models {
		right += m.Correct

		fmt.Fprintf(&b, "  %s: answered %d; %d right, %d wrong (%s)\n",
			strings.TrimSpace(m.Backend+" "+m.Model), m.Answered, m.Correct, m.Answered-m.Correct,
			percent(m.Correct, m.Answered))
	}

	fmt.Fprintf(&b, "  fallback: %d got %q; %d right\n", r.FellBack.Answered, r.Fallback, r.FellBack.Correct)

	for _, reason := range slices.Sorted(maps.Keys(r.Reasons)) {
		fmt.Fprintf(&b, "    %d: %s\n", r.Reasons[reason], reason)
	}

	fmt.Fprintf(&b, "  with no model: %q for all %d would be right %d times (%s)\n",
		r.Fallback, left, r.LeftForFallback, percent(r.LeftForFallback, left))

	fmt.Fprintf(&b, "\noverall: %d of %d right (%s)\n", right, r.Total, percent(right, r.Total))

	r.renderWrong(&b)

	return b.String()
}

// renderWrong lists the misses of the rules, then of each model, then the
// cases that fell back and were labeled otherwise.
func (r Report) renderWrong(b *strings.Builder) {
	var order []string

	for _, m := range r.Wrong {
		if !slices.Contains(order, m.Backend) {
			order = append(order, m.Backend)
		}
	}

	slices.SortStableFunc(order, func(a, b string) int { return backendRank(a) - backendRank(b) })

	for _, backend := range order {
		rows := [][]string{{"want", "got", "state"}}

		for _, m := range r.Wrong {
			if m.Backend == backend {
				rows = append(rows, []string{m.Want, m.Got, m.State})
			}
		}

		fmt.Fprintf(b, "\nwrong by %s:\n", backend)
		writeTable(b, rows)
	}
}

// writeTable pads every column but the last to its widest cell.
func writeTable(b *strings.Builder, rows [][]string) {
	widths := make([]int, len(rows[0]))

	for _, row := range rows {
		for i, cell := range row {
			widths[i] = max(widths[i], utf8.RuneCountInString(cell))
		}
	}

	for _, row := range rows {
		for i, cell := range row {
			b.WriteString(cell)

			if i < len(row)-1 {
				b.WriteString(strings.Repeat(" ", widths[i]-utf8.RuneCountInString(cell)+tablePadding))
			}
		}

		b.WriteString("\n")
	}
}

const (
	// The space between rendered columns.
	tablePadding = 2
	// How much of a state a wrong case shows, and how much is read to
	// find that much once its whitespace is collapsed.
	excerptLimit = 100
	excerptRead  = 2 * excerptLimit
	// What turns a ratio into a percentage.
	percentScale = 100
)

// The order wrong cases are listed in, which is the order a decision is
// tried.
const (
	rankRule = iota
	rankModel
	rankFallback
)

func backendRank(backend string) int {
	switch backend {
	case BackendRule:
		return rankRule
	case BackendFallback:
		return rankFallback
	default:
		return rankModel
	}
}

func percent(part, whole int) string {
	if whole == 0 {
		return "-"
	}

	return fmt.Sprintf("%.1f%%", percentScale*float64(part)/float64(whole))
}

// excerpt is the start of a state with its whitespace collapsed, so one
// case stays on one row.
func excerpt(state string) string {
	flat := strings.Join(strings.Fields(truncate(state, excerptRead)), " ")
	if len(flat) <= excerptLimit {
		return flat
	}

	return truncate(flat, excerptLimit) + "..."
}
