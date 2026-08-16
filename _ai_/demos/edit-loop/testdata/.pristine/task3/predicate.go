// Package predicate is a trimmed copy of gh-repo-dashboard's
// internal/filters/predicate.go ParseError type, adapted for the edit-loop
// spike.
package predicate

import "fmt"

// ParseError reports a filter predicate expression that failed to parse.
type ParseError struct {
	Input   string
	Message string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("parsing %q: %s", e.Input, e.Message)
}
