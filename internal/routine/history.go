package routine

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/kyleking/wavez/internal/gate"
)

// Status names how one step ended.
type Status string

// Statuses a StepRecord may carry.
const (
	StatusPass Status = "pass"
	// StatusFail is a step whose action reported a failure, trimmed the
	// same way a gate failure is.
	StatusFail Status = "fail"
	// StatusSkipped is a step whose parent did not pass, so it never ran.
	StatusSkipped Status = "skipped"
	// StatusCanceled is a step stopped by a newer run taking its
	// concurrency key, or by the run's context ending.
	StatusCanceled Status = "canceled"
	// StatusError is a step whose action could not run at all, which is a
	// different thing from the check it performs failing.
	StatusError Status = "error"
)

// StepRecord is one step's outcome in a run.
type StepRecord struct {
	Name     string                `json:"name"`
	Action   string                `json:"action"`
	Status   Status                `json:"status"`
	Error    string                `json:"error,omitempty"`
	Failures []gate.TrimmedFailure `json:"failures,omitempty"`
	Duration time.Duration         `json:"duration"`
	Examined int                   `json:"examined"`
}

// RunRecord is one routine run, the unit the history file stores and the
// routines panel renders.
type RunRecord struct {
	Timestamp time.Time     `json:"timestamp"`
	Routine   string        `json:"routine"`
	Trigger   Trigger       `json:"trigger"`
	Steps     []StepRecord  `json:"steps"`
	Duration  time.Duration `json:"duration"`
	Pass      bool          `json:"pass"`
}

const historyFilePerm = 0o600

// History is a per-project, append-only JSON-lines record of routine runs,
// the routine-side counterpart of gate.Log. It holds the same trimmed
// output a gate failure carries and nothing wider.
type History struct {
	path string
	mu   sync.Mutex
}

// OpenHistory opens (creating if absent) the routine history at path.
func OpenHistory(path string) (*History, error) {
	//nolint:gosec // path is a caller-configured project file
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, historyFilePerm)
	if err != nil {
		return nil, fmt.Errorf("opening routine history %s: %w", path, err)
	}

	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("closing routine history %s: %w", path, err)
	}

	return &History{path: path}, nil
}

// Append writes one run to the history. A nil History discards, so a caller
// with nowhere to write still runs routines.
func (h *History) Append(rec RunRecord) error {
	if h == nil {
		return nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	f, err := os.OpenFile(h.path, os.O_APPEND|os.O_WRONLY, historyFilePerm)
	if err != nil {
		return fmt.Errorf("opening routine history %s: %w", h.path, err)
	}
	defer func() { _ = f.Close() }() //nolint:errcheck // append-only write already flushed by Encode below

	if err := json.NewEncoder(f).Encode(rec); err != nil {
		return fmt.Errorf("appending routine history entry: %w", err)
	}

	return nil
}

// Runs reads every recorded run in order.
func (h *History) Runs() ([]RunRecord, error) {
	if h == nil {
		return nil, nil
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	f, err := os.Open(h.path)
	if err != nil {
		return nil, fmt.Errorf("opening routine history %s: %w", h.path, err)
	}
	defer func() { _ = f.Close() }() //nolint:errcheck // read-only handle, nothing actionable on close failure

	var runs []RunRecord

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, historyInitialBuf), historyMaxBuf)

	for sc.Scan() {
		var rec RunRecord
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			return nil, fmt.Errorf("parsing routine history entry: %w", err)
		}

		runs = append(runs, rec)
	}

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading routine history %s: %w", h.path, err)
	}

	return runs, nil
}

const (
	historyInitialBuf = 64 << 10
	historyMaxBuf     = 1 << 20
)

// Recent returns the last n runs of routine name, oldest first.
func (h *History) Recent(name string, n int) ([]RunRecord, error) {
	runs, err := h.Runs()
	if err != nil {
		return nil, err
	}

	var mine []RunRecord

	for _, r := range runs {
		if r.Routine == name {
			mine = append(mine, r)
		}
	}

	if len(mine) > n {
		mine = mine[len(mine)-n:]
	}

	return mine, nil
}
