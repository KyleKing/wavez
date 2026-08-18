package cycle

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Hypothesis is one ledger row: a candidate cause, the experiment that
// tested it, what was observed, and the verdict. Measured over 11 real
// transcripts, this is the 2.4% no tool can produce again, which is why it
// is what crosses a phase boundary while the prose does not.
//
// No Condition reads a Hypothesis. The harness carries what the model wrote
// and checks the tree itself, so a row can be wrong without a phase
// advancing on it.
type Hypothesis struct {
	Phase       string `json:"phase"`
	Cause       string `json:"cause"`
	Experiment  string `json:"experiment"`
	Observation string `json:"observation"`
	Verdict     string `json:"verdict"`
}

// Dismissal accounts for one sweep hit the run decided to leave alone.
type Dismissal struct {
	File   string `json:"file"`
	Reason string `json:"reason"`
	Line   int    `json:"line"`
}

// Sweep is a recorded structural sweep for a proved cause. Artifact names
// the durable thing that replaces it when the pattern does not discriminate:
// measured in _ai_/demos/pattern-sweep, a local syntactic cause resolved to
// four hits with no false positives while a dataflow cause returned 100 hits
// of noise, so the generalize phase has to accept a helper or a boundary
// test in place of a work list.
type Sweep struct {
	Phase     string      `json:"phase"`
	Pattern   string      `json:"pattern"`
	Language  string      `json:"language"`
	Path      string      `json:"path"`
	Artifact  string      `json:"artifact,omitempty"`
	Dismissed []Dismissal `json:"dismissed,omitempty"`
}

// Rows is what a phase carries forward beside the standing goal and the
// change set.
type Rows struct {
	Hypotheses []Hypothesis
	Sweeps     []Sweep
}

// Empty reports whether any row has been recorded.
func (r Rows) Empty() bool { return len(r.Hypotheses) == 0 && len(r.Sweeps) == 0 }

// LastSweep returns the most recently recorded sweep, and false when none
// was.
func (r Rows) LastSweep() (Sweep, bool) {
	if len(r.Sweeps) == 0 {
		return Sweep{}, false
	}

	return r.Sweeps[len(r.Sweeps)-1], true
}

// Ledger collects the rows a Cycle's phases record. It is handed to the
// tools a phase runs with, so it is safe for concurrent use.
type Ledger struct {
	phase string
	rows  Rows
	mu    sync.Mutex
}

// NewLedger builds an empty Ledger.
func NewLedger() *Ledger { return &Ledger{} }

// SetPhase names the phase later rows are attributed to.
func (l *Ledger) SetPhase(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.phase = name
}

// RecordHypothesis appends one ledger row.
func (l *Ledger) RecordHypothesis(h Hypothesis) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	h.Phase = l.phase
	l.rows.Hypotheses = append(l.rows.Hypotheses, h)

	return nil
}

// RecordSweep appends one sweep, merging its dismissals into an earlier
// sweep of the same pattern so a run that triages across several calls is
// not asked to repeat what it already accounted for.
func (l *Ledger) RecordSweep(s Sweep) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	s.Phase = l.phase

	for i := range l.rows.Sweeps {
		if l.rows.Sweeps[i].Pattern != s.Pattern {
			continue
		}

		merged := mergeDismissals(l.rows.Sweeps[i].Dismissed, s.Dismissed)
		s.Dismissed = merged

		if s.Artifact == "" {
			s.Artifact = l.rows.Sweeps[i].Artifact
		}

		l.rows.Sweeps = append(append(l.rows.Sweeps[:i], l.rows.Sweeps[i+1:]...), s)

		return nil
	}

	l.rows.Sweeps = append(l.rows.Sweeps, s)

	return nil
}

// Rows returns a copy of everything recorded so far.
func (l *Ledger) Rows() Rows {
	l.mu.Lock()
	defer l.mu.Unlock()

	return Rows{
		Hypotheses: append([]Hypothesis{}, l.rows.Hypotheses...),
		Sweeps:     append([]Sweep{}, l.rows.Sweeps...),
	}
}

func mergeDismissals(into, from []Dismissal) []Dismissal {
	out := append([]Dismissal{}, into...)

	for _, d := range from {
		replaced := false

		for i := range out {
			if out[i].File == d.File && out[i].Line == d.Line {
				out[i] = d
				replaced = true

				break
			}
		}

		if !replaced {
			out = append(out, d)
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}

		return out[i].Line < out[j].Line
	})

	return out
}

// Render writes the rows as the lines a phase prompt carries. It is the
// carried context in full: 360 tokens for a session's largest
// investigation, against the 79.5k of transcript it stands in for.
func (r Rows) Render() string {
	var b strings.Builder

	if len(r.Hypotheses) > 0 {
		b.WriteString("## Hypothesis ledger\n\n")
		b.WriteString("| phase | candidate cause | experiment | observation | verdict |\n")
		b.WriteString("|---|---|---|---|---|\n")

		for _, h := range r.Hypotheses {
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n",
				h.Phase, h.Cause, h.Experiment, h.Observation, h.Verdict)
		}

		b.WriteString("\n")
	}

	for _, s := range r.Sweeps {
		fmt.Fprintf(&b, "## Sweep recorded in %s\n\npattern: %s (%s) over %s\n", s.Phase, s.Pattern, s.Language, s.Path)

		if s.Artifact != "" {
			fmt.Fprintf(&b, "durable artifact: %s\n", s.Artifact)
		}

		for _, d := range s.Dismissed {
			fmt.Fprintf(&b, "dismissed %s:%d: %s\n", d.File, d.Line, d.Reason)
		}

		b.WriteString("\n")
	}

	return b.String()
}
