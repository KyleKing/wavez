package astgrep

import "fmt"

// ModelFinding is the only view of a Finding a model is allowed to see:
// rule id, message, file:line, and the fix hunk, per DESIGN.md's
// Structural rules section.
type ModelFinding struct {
	RuleID   string
	Message  string
	Location string
	Fix      string
}

// TrimForModel reduces a Finding to ModelFinding. It is pure so the
// trimming rule can be tested without ever shelling out to ast-grep.
func TrimForModel(f Finding) ModelFinding {
	return ModelFinding{
		RuleID:   f.RuleID,
		Message:  f.Message,
		Location: fmt.Sprintf("%s:%d", f.File, f.Start.Line),
		Fix:      f.Fix,
	}
}
