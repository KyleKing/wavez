package decide

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
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

// BackendCount is how one backend did over a corpus.
type BackendCount struct {
	// Backend is BackendRule, BackendCache, BackendFallback, or a model
	// backend's name.
	Backend string
	// Answered is how many cases this backend decided.
	Answered int
	// Correct is how many of those matched the label.
	Correct int
}

// Miss is one case the Decider got wrong.
type Miss struct {
	Want    string
	Got     string
	Backend string
	// State is the start of the case's state on one line.
	State string
}

// Report is the result of running a corpus through a Decider.
type Report struct {
	Question string
	// Backends is in the order a decision is tried: rule, cache, each
	// model backend by name, then fallback. A backend that answered
	// nothing is left out.
	Backends []BackendCount
	// Wrong lists the missed cases in corpus order.
	Wrong []Miss
	// Total is how many cases were decided, and Correct how many of them
	// matched their label.
	Total   int
	Correct int
	// Skipped is how many cases were left when the context ended.
	Skipped int
}

// Accuracy is the share of decided cases that matched their label, from 0
// to 1. An empty report has an accuracy of 0.
func (r Report) Accuracy() float64 {
	return ratio(r.Correct, r.Total)
}

// Evaluate decides every case with d and compares the choice with the
// case's label. It reads what each backend took and how often it was right:
// a rule that is wrong on the corpus should not fire, a model that does no
// better than the fallback should not be asked, and a question nothing
// answers well should stay in code. A case labeled with a key outside q's
// choices counts as wrong. Evaluate stops early when ctx ends and reports
// the rest as skipped.
func Evaluate(ctx context.Context, d Decider, q Question, rules []Rule, cases []Case) Report {
	report := Report{Question: q.Name}
	counts := map[string]*BackendCount{}

	for i, c := range cases {
		if ctx.Err() != nil {
			report.Skipped = len(cases) - i

			break
		}

		got := d.Decide(ctx, q, c.State, rules...)

		count := counts[got.Backend]
		if count == nil {
			count = &BackendCount{Backend: got.Backend}
			counts[got.Backend] = count
		}

		report.Total++
		count.Answered++

		if got.Choice == c.Want {
			report.Correct++
			count.Correct++

			continue
		}

		report.Wrong = append(report.Wrong, Miss{
			Want: c.Want, Got: got.Choice, Backend: got.Backend, State: excerpt(c.State),
		})
	}

	for _, count := range counts {
		report.Backends = append(report.Backends, *count)
	}

	slices.SortFunc(report.Backends, func(a, b BackendCount) int {
		if byRank := backendRank(a.Backend) - backendRank(b.Backend); byRank != 0 {
			return byRank
		}

		return strings.Compare(a.Backend, b.Backend)
	})

	return report
}

// Render prints the report as plain text: a summary line, a row per
// backend, and a row per wrong case.
func (r Report) Render() string {
	var b strings.Builder

	fmt.Fprintf(&b, "question: %s\n", r.Question)
	fmt.Fprintf(&b, "cases: %d  correct: %d  accuracy: %s\n", r.Total, r.Correct, percent(r.Correct, r.Total))

	if r.Skipped > 0 {
		fmt.Fprintf(&b, "skipped: %d (stopped early)\n", r.Skipped)
	}

	if len(r.Backends) > 0 {
		rows := make([][]string, 0, 1+len(r.Backends))
		rows = append(rows, []string{"backend", "answered", "correct", "accuracy"})

		for _, c := range r.Backends {
			rows = append(rows, []string{
				c.Backend, strconv.Itoa(c.Answered), strconv.Itoa(c.Correct), percent(c.Correct, c.Answered),
			})
		}

		b.WriteString("\n")
		writeTable(&b, rows)
	}

	if len(r.Wrong) > 0 {
		rows := make([][]string, 0, 1+len(r.Wrong))
		rows = append(rows, []string{"want", "got", "backend", "state"})

		for _, m := range r.Wrong {
			rows = append(rows, []string{m.Want, m.Got, m.Backend, m.State})
		}

		b.WriteString("\nwrong:\n")
		writeTable(&b, rows)
	}

	return b.String()
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
	excerptLimit = 80
	excerptRead  = 2 * excerptLimit
	// What turns a ratio into a percentage.
	percentScale = 100
)

// The order backends report in, which is the order a decision tries them.
const (
	rankRule = iota
	rankCache
	rankModel
	rankFallback
)

func backendRank(backend string) int {
	switch backend {
	case BackendRule:
		return rankRule
	case BackendCache:
		return rankCache
	case BackendFallback:
		return rankFallback
	default:
		return rankModel
	}
}

func ratio(part, whole int) float64 {
	if whole == 0 {
		return 0
	}

	return float64(part) / float64(whole)
}

func percent(part, whole int) string {
	if whole == 0 {
		return "-"
	}

	return fmt.Sprintf("%.1f%%", percentScale*ratio(part, whole))
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
