package gate

import (
	"context"
	"errors"
)

// ErrNotBuilt marks the skeleton; the gate lane replaces this file.
var ErrNotBuilt = errors.New("gate: not built yet")

// Failed reports whether any blocking check failed.
func (r Report) Failed() bool { return false }

// Fingerprint identifies the set of blocking failures, so a caller can tell
// a repeated failure from a new one. It is empty when nothing blocks.
func (r Report) Fingerprint() string { return "" }

// Render is the text an agent reads. It is empty when there is nothing the
// agent needs to know.
func (r Report) Render() string { return "" }

// New returns the Runner that loads the project's configuration from root on
// each run.
func New() Runner { return stubRunner{} }

type stubRunner struct{}

func (stubRunner) Run(context.Context, string, []string, Stage) (Report, error) {
	return Report{}, ErrNotBuilt
}
