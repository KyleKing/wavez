package bench

import (
	"context"
	"time"
)

// Request is one task handed to a harness.
type Request struct {
	// Dir is the workspace the harness works in.
	Dir    string
	Prompt string
	// Model is the model to ask for, empty for the harness's default.
	Model string
	// SessionID is the id the harness should give the session, so its
	// transcript can be found even when the run is cut off. A harness that
	// cannot name its sessions ignores it.
	SessionID string
	// Env is the child's whole environment.
	Env []string
}

// Usage is the tokens a run spent.
type Usage struct {
	Input         int `json:"input"`
	Output        int `json:"output"`
	CacheRead     int `json:"cache_read"`
	CacheCreation int `json:"cache_creation"`
}

// Outcome is what a harness reported about one run. A harness that cannot
// know a field leaves it zero.
type Outcome struct {
	// Answer is the harness's final text.
	Answer string
	// SessionID names the transcript the run left, when the harness keeps
	// one.
	SessionID string
	// Served is the model that actually answered, which is not always the
	// one asked for.
	Served string
	// Stop is why the run ended: StopCompleted, or the harness's own word
	// for what went wrong.
	Stop     string
	Usage    Usage
	Turns    int
	CostUSD  float64
	Duration time.Duration
}

// Stop reasons this package assigns itself.
const (
	// StopCompleted is a run the harness finished without reporting an
	// error.
	StopCompleted = "completed"
	// StopError is a run the harness reported as failed without saying how.
	StopError = "error"
	// StopTimeout is a run cut off by the runner's deadline.
	StopTimeout = "timeout"
	// StopHarnessError is a run whose harness could not be started or
	// understood, so nothing it cost is known.
	StopHarnessError = "harness_error"
)

// Harness runs one prompt in one directory. An error means the run could
// not be measured; a run the harness itself reports as failed is an Outcome
// whose Stop says so.
type Harness interface {
	Run(ctx context.Context, req Request) (Outcome, error)
}
