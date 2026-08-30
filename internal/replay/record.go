package replay

import (
	"bufio"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"time"

	"github.com/kyleking/wavez/internal/bench"
)

// DefaultRecordsPath is where this repo keeps the records, relative to the
// project root.
const DefaultRecordsPath = "_ai_/bench/replay/records.jsonl"

const (
	recordsDirMode  = 0o750
	recordsFileMode = 0o600
)

// Run is what one replay was asked to do. It rides on the record because a
// comparison that pairs two runs given different caps or different tiers
// measures the caps, and nothing in the counters says so.
type Run struct {
	// Served names the model behind each tier and where it answered, because
	// Model is a tier name and says nothing about what served it. A fast
	// tier moved from a local llama-server to a hosted endpoint keeps the
	// same tier name and is a different machine, a different window, and
	// possibly no grammar at all, so a comparison across that move measures
	// the move.
	Served map[string]string `json:"served,omitempty"`
	Task   string            `json:"task"`
	Label  string            `json:"label"`
	Model  string            `json:"model"`
	// TaskHash identifies the prompt text, so a report can say that the task
	// itself changed between two records of the same id.
	TaskHash string `json:"task_hash"`
	MaxTurns int    `json:"max_turns"`
}

// SameSetup reports whether two runs were asked for the same thing, which is
// what makes their counters comparable. A record written before Served
// existed carries none and is compared on the rest.
func (r Run) SameSetup(other Run) bool {
	return r.Model == other.Model && r.MaxTurns == other.MaxTurns &&
		(len(r.Served) == 0 || len(other.Served) == 0 || maps.Equal(r.Served, other.Served))
}

// StopComplete is the stop reason of a run the loop ended on its own terms,
// as opposed to a deadline, a stall, or an error.
const StopComplete = "complete"

// Record is one run of one task: what it was asked to do, how it ended, and
// what it spent. Stats is the same summary -stats prints, so a record and a
// live thread are read the same way.
type Record struct {
	Run
	Started string        `json:"started"`
	Stop    string        `json:"stop"`
	Checks  []CheckResult `json:"checks,omitempty"`
	Stats   bench.Stats   `json:"stats"`
	// SpendUSD is what the run's hosted turns cost, accumulated by the loop
	// against the model that actually served each turn. Stats carries the
	// tokens and not the model behind them, so this is the only place a
	// record says what it cost.
	SpendUSD float64 `json:"spend_usd,omitempty"`
	// Complete is the loop's own verdict, which says the run ended tidily
	// and nothing about whether it did the task. Checks is what says that.
	Complete bool `json:"complete"`
}

// CheckSummary is the checks as a column: how many held, or a dash for a
// task that asserts nothing.
func (r Record) CheckSummary() string {
	if len(r.Checks) == 0 {
		return "-"
	}

	passed := 0
	for _, c := range r.Checks {
		if c.Pass {
			passed++
		}
	}

	return fmt.Sprintf("%d/%d", passed, len(r.Checks))
}

// NewRecord stamps a finished run.
func NewRecord(
	run Run, started time.Time, stop string,
	stats bench.Stats, checks []CheckResult, spendUSD float64,
) Record {
	return Record{
		Run:      run,
		Started:  started.UTC().Format(time.RFC3339),
		Stop:     stop,
		Complete: stop == StopComplete,
		Stats:    stats,
		Checks:   checks,
		SpendUSD: spendUSD,
	}
}

// Append adds one record to the file, creating it and its directory. Records
// are append-only: a run that already happened is not rewritten by a later
// one, so a lane keeps every attempt it made rather than its best.
func Append(path string, r Record) error {
	if err := os.MkdirAll(filepath.Dir(path), recordsDirMode); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}

	line, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("encoding record for task %s: %w", r.Task, err)
	}

	//nolint:gosec // path names a records file the caller chose
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, recordsFileMode)
	if err != nil {
		return fmt.Errorf("opening %s: %w", path, err)
	}
	defer func() { _ = f.Close() }() //nolint:errcheck // the write below is what can fail meaningfully

	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("appending to %s: %w", path, err)
	}

	return nil
}

// Load reads every record in the file, oldest first. A file that does not
// exist yet holds no records, which is not an error.
func Load(path string) ([]Record, error) {
	f, err := os.Open(path) //nolint:gosec // path is the caller's
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer func() { _ = f.Close() }() //nolint:errcheck // read-only handle

	var out []Record

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), maxRecordLine)

	for sc.Scan() {
		var r Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}

		out = append(out, r)
	}

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	return out, nil
}

// maxRecordLine bounds one record, which carries a run's whole shell history.
const maxRecordLine = 4 << 20

// ForTask keeps the records of one task, oldest first.
func ForTask(recs []Record, task string) []Record {
	var out []Record
	for i := range recs {
		if recs[i].Task == task {
			out = append(out, recs[i])
		}
	}

	return out
}

// NeverPassed reports the number of runs of this task version that were
// checked against check and the number that satisfied it.
//
// A check no run has ever passed is a defect in the check often enough to
// be worth saying out loud: `h11` asked for the word "overlap" in a Go test
// file, where the language capitalizes it, so four runs in a row were
// reported partial for work that was correct.
func NeverPassed(recs []Record, r Record, check string) (int, int) {
	runs, passed := 0, 0

	for i := range recs {
		if recs[i].Task != r.Task || recs[i].TaskHash != r.TaskHash {
			continue
		}

		for _, c := range recs[i].Checks {
			if c.Check != check {
				continue
			}

			runs++

			if c.Pass {
				passed++
			}
		}
	}

	return runs, passed
}
