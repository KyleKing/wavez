package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kyleking/wavez/internal/cycle"
	"github.com/kyleking/wavez/internal/tool"
)

// maxSweepHits bounds what one sweep call returns to the model. A pattern
// that matches more than this is noise rather than a work list, which is the
// measured failure mode for a cause that spans functions.
const maxSweepHits = 40

var sweepSchema = buildSchema(map[string]schemaProperty{
	"pattern": {
		Type: schemaTypeString,
		Description: "The proved cause as one ast-grep structural pattern, e.g. " +
			"'if len($C) == 0 { return $T{$$$A, Pass: true}, nil }'. Metavariables are $NAME " +
			"for one node and $$$NAME for a list.",
	},
	"language": {Type: schemaTypeString, Description: "Language to match, defaulting to go."},
	"path": {
		Type:        schemaTypeString,
		Description: "Directory to sweep, relative to the project root. Defaults to the whole project.",
	},
	"dismiss_file": {
		Type:        schemaTypeString,
		Description: "A hit you are leaving alone, as the file path the sweep reported.",
	},
	"dismiss_line": {
		Type:        schemaTypeInteger,
		Description: "The line of the hit being dismissed. Omit to dismiss every hit in the file.",
	},
	"dismiss_reason": {
		Type:        schemaTypeString,
		Description: "Why that hit is correct as written. Required with dismiss_file.",
	},
	"artifact": {
		Type: schemaTypeString,
		Description: "The durable file you wrote instead, when the pattern does not discriminate: " +
			"a rule file, a helper, or a boundary test. It must be a file this run changed.",
	},
}, "pattern")

// SweepRecorder records a phase's sweeps and reports what it has recorded so
// far. *cycle.Ledger satisfies it.
type SweepRecorder interface {
	RecordSweep(s cycle.Sweep) error
	Rows() cycle.Rows
}

// Sweep enumerates the sibling sites of a proved cause and records how each
// is accounted for. The hit list comes from the harness rather than from the
// model, because "where else does this happen" is the recall question a
// model is worst at and a confident answer is indistinguishable from a
// complete one.
type Sweep struct {
	sweeper  cycle.Sweeper
	recorder SweepRecorder
	root     string
}

// NewSweep builds a Sweep tool over root, matching through sweeper and
// recording into recorder.
func NewSweep(root string, sweeper cycle.Sweeper, recorder SweepRecorder) *Sweep {
	return &Sweep{root: root, sweeper: sweeper, recorder: recorder}
}

// Name implements tool.Tool.
func (*Sweep) Name() string { return "sweep" }

// Description implements tool.Tool.
func (*Sweep) Description() string {
	return "Match a structural pattern across the project and return every site it hits, so a " +
		"proved cause can be generalized against a list rather than from memory. Call it again " +
		"with dismiss_file and dismiss_reason for each hit that is correct as written. A hit you " +
		"fix stops matching and needs no dismissal."
}

// Schema implements tool.Tool.
func (*Sweep) Schema() json.RawMessage { return sweepSchema }

type sweepInput struct {
	Pattern       string `json:"pattern"`
	Language      string `json:"language"`
	Path          string `json:"path"`
	DismissFile   string `json:"dismiss_file"`
	DismissReason string `json:"dismiss_reason"`
	Artifact      string `json:"artifact"`
	DismissLine   int    `json:"dismiss_line"`
}

// Run implements tool.Tool.
func (s *Sweep) Run(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, fmt.Errorf("sweep: %w", err)
	}

	var in sweepInput
	if err := decodeInput(input, &in); err != nil {
		return tool.Fail(tool.CauseMalformed, "invalid input: %v", err), nil
	}

	if in.Pattern == "" {
		return tool.Fail(tool.CauseBadInput, "pattern is required"), nil
	}

	if in.DismissFile != "" && in.DismissReason == "" {
		return tool.Fail(tool.CauseBadInput,
			"dismiss_reason is required with dismiss_file: a hit left alone needs a reason"), nil
	}

	record := cycle.Sweep{Pattern: in.Pattern, Language: in.Language, Path: in.Path, Artifact: in.Artifact}
	if in.DismissFile != "" {
		record.Dismissed = []cycle.Dismissal{
			{File: in.DismissFile, Line: in.DismissLine, Reason: in.DismissReason},
		}
	}

	if err := s.recorder.RecordSweep(record); err != nil {
		return tool.Fail(tool.CauseIO, "could not record the sweep: %v", err), nil
	}

	hits, err := s.sweeper.Sweep(ctx, s.root, record)
	if err != nil {
		return tool.Fail(tool.CauseUpstream, "the sweep could not run: %v", err), nil
	}

	return tool.Result{Content: s.report(in.Pattern, hits)}, nil
}

// report lists the hits still waiting on a decision, since a hit already
// accounted for is not work.
func (s *Sweep) report(pattern string, hits []cycle.Hit) string {
	dismissed := dismissalsFor(s.recorder.Rows(), pattern)

	var b strings.Builder

	fmt.Fprintf(&b, "%d hit(s) for the pattern, %d already accounted for.\n", len(hits), len(dismissed))

	shown := 0

	for _, h := range hits {
		if coveredBy(dismissed, h) {
			continue
		}

		if shown == maxSweepHits {
			fmt.Fprintf(&b, "… and more. A pattern matching this widely is not a work list: "+
				"narrow it, or name a durable artifact instead.\n")

			break
		}

		fmt.Fprintf(&b, "%s:%d\n", h.File, h.Line)
		shown++
	}

	if shown == 0 {
		b.WriteString("Nothing is left untriaged.\n")
	}

	return b.String()
}

func dismissalsFor(rows cycle.Rows, pattern string) []cycle.Dismissal {
	for _, s := range rows.Sweeps {
		if s.Pattern == pattern {
			return s.Dismissed
		}
	}

	return nil
}

func coveredBy(dismissed []cycle.Dismissal, h cycle.Hit) bool {
	for _, d := range dismissed {
		if d.File == h.File && (d.Line == 0 || d.Line == h.Line) {
			return true
		}
	}

	return false
}
