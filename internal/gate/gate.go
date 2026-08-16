package gate

import (
	"context"
	"time"

	"github.com/kyleking/wavez/internal/tool"
)

// Level names which selection tier resolved a Selection. DESIGN.md's Gates
// section orders these line, importer, package; a level is only used when
// the tier above it could not decide.
type Level string

// Levels Select may return, in the order DESIGN.md's Gates section tries
// them.
const (
	LevelLine     Level = "line"
	LevelImporter Level = "importer"
	LevelPackage  Level = "package"
)

// Selection is what a batch of changes resolved to: either specific test
// IDs (LevelLine) or whole Go packages to run (LevelImporter, LevelPackage).
type Selection struct {
	Level    Level
	Tests    []string
	Packages []string
}

// RunContext is what one debounced batch hands to every Gate.
type RunContext struct {
	RepoRoot  string
	Changes   []tool.Change
	Selection Selection
}

// TrimmedFailure is what reaches the model on a gate failure: a test name
// and only the output lines that reference a changed file. DESIGN.md's
// Gates section calls this the whole point of a gate: a passing run gives
// the model nothing but a boolean, a failing one gives it only what touches
// the change.
type TrimmedFailure struct {
	Test    string
	Package string
	Frames  []string
}

// Result is one Gate's outcome. Timestamp and Duration are stamped by
// RunGates, not by the Gate itself, so gates stay free of clock plumbing.
type Result struct {
	Timestamp time.Time
	Gate      string
	Level     Level
	Failures  []TrimmedFailure
	Duration  time.Duration
	TestCount int
	Pass      bool
}

// ModelView is the asymmetric, trimmed view of a Result a model
// actually sees: nothing but pass/fail and a timestamp on success, failing
// test names and their changed-file frames on failure.
type ModelView struct {
	Timestamp time.Time
	Failures  []TrimmedFailure
	Pass      bool
}

// ForModel reduces a Result to what the model is allowed to see.
func (r Result) ForModel() ModelView {
	if r.Pass {
		return ModelView{Pass: true, Timestamp: r.Timestamp}
	}

	return ModelView{Pass: false, Timestamp: r.Timestamp, Failures: r.Failures}
}

// Gate is one deterministic check a Runner triggers on a change event.
// Resources lists the keys this Gate reads or mutates exclusively; two
// Gates that share a key never run concurrently in RunGates.
type Gate interface {
	Name() string
	Resources() []string
	Run(ctx context.Context, rc RunContext) (Result, error)
}
