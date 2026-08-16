package stakes

import "github.com/kyleking/wavez/internal/guard"

// Band is the coarse escalation tier a Score maps to: what a permission
// prompt renders and what a model tier picker may key on. It is evidence,
// never a decision.
type Band string

const (
	// BandLow means no computed signal found reason for extra caution.
	BandLow Band = "low"
	// BandModerate means one signal (a guard approval requirement, an
	// unresolved reversibility check) warrants a closer look.
	BandModerate Band = "moderate"
	// BandHigh means a signal proved real cost: a new capability, an
	// irreversible path, or a guard refusal.
	BandHigh Band = "high"
)

const (
	rankLow = iota
	rankModerate
	rankHigh
)

func (b Band) rank() int {
	switch b {
	case BandHigh:
		return rankHigh
	case BandModerate:
		return rankModerate
	case BandLow:
		return rankLow
	default:
		return rankLow
	}
}

// Edit is the before and after content of one file a change touches, the
// input capability-delta detection needs. Before is empty for a newly
// created file.
type Edit struct {
	Path   string
	Before string
	After  string
}

// Input is what Compute needs to score one pending action. Every field is
// optional; a caller that cannot supply one degrades the corresponding
// signal to unknown rather than to safe. Guard is nil when the action is
// not a shell command guard.Classify already ran on.
type Input struct {
	Guard       *guard.Verdict
	ProjectRoot string
	Paths       []string
	Edits       []Edit
}

// Score is the deterministic evidence Compute produces. Fields track
// whether a signal was actually checked, since an unchecked signal must
// render as unknown rather than as the safe value.
type Score struct {
	Guard         *guard.Verdict `json:"guard,omitempty"`
	Band          Band           `json:"band"`
	Reversibility Reversibility  `json:"reversibility"`
	Capabilities  []Capability   `json:"capabilities,omitempty"`
	BlastRadius   int            `json:"blast_radius,omitempty"`
	// EditedFiles is how many distinct files the scored change set touches,
	// which is the scope Capabilities was computed over: "no new capability"
	// reads very differently across one file than across forty.
	EditedFiles int  `json:"edited_files,omitempty"`
	CapsChecked bool `json:"caps_checked"`
	BlastKnown  bool `json:"blast_known"`
}

// Compute scores in from its inputs alone: same Input, same Score, every
// time. It touches no filesystem, spawns no process, and never consults a
// model.
func Compute(in Input) Score {
	caps, checked := capabilityDelta(in.Edits)

	score := Score{
		Capabilities:  caps,
		CapsChecked:   checked,
		EditedFiles:   distinctPaths(in.Edits),
		Reversibility: reversibilityOf(in.ProjectRoot, in.Paths),
		Guard:         in.Guard,
		// BlastRadius is a declared seam: internal/codeintel's edges table
		// (DESIGN.md "Code intelligence") has no writer yet, so transitive
		// importer counts are not available. BlastKnown stays false until
		// that adapter lands, and Render must say "unknown", never "0".
		BlastKnown: false,
	}
	score.Band = bandFor(score)

	return score
}

func distinctPaths(edits []Edit) int {
	seen := make(map[string]struct{}, len(edits))
	for _, e := range edits {
		seen[e.Path] = struct{}{}
	}

	return len(seen)
}

func bandFor(s Score) Band {
	band := BandLow

	if len(s.Capabilities) > 0 {
		band = raise(band, BandHigh)
	}

	if s.Reversibility == Irreversible {
		band = raise(band, BandHigh)
	}

	if s.Guard != nil {
		switch *s.Guard {
		case guard.Refuse:
			band = raise(band, BandHigh)
		case guard.NeedsApproval:
			band = raise(band, BandModerate)
		case guard.Allow:
		}
	}

	return band
}

func raise(current, candidate Band) Band {
	if candidate.rank() > current.rank() {
		return candidate
	}

	return current
}
