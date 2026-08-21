package gate

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// LogEntry is one recorded gate outcome.
type LogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Gate      string    `json:"gate"`
	Level     Level     `json:"level"`
	Reason    string    `json:"reason,omitempty"`
	// Advisories records findings that did not fail the gate, so a weak test
	// or a surviving mutant is auditable after the fact.
	Advisories []TrimmedFailure `json:"advisories,omitempty"`
	Duration   time.Duration    `json:"duration"`
	Examined   int              `json:"examined"`
	Pass       bool             `json:"pass"`
}

const logFilePerm = 0o600

// Log is a per-project, append-only JSON-lines record of gate outcomes:
// pass/fail, timestamp, duration, selection level, and how many units the
// gate examined, exactly what DESIGN.md's Gates section says a gate log
// holds. The examined count is what makes an abstention auditable after the
// fact rather than indistinguishable from a clean run.
type Log struct {
	path string
	mu   sync.Mutex
}

// OpenLog opens (creating if absent) the gate log at path.
func OpenLog(path string) (*Log, error) {
	//nolint:gosec // path is a caller-configured project file
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, logFilePerm)
	if err != nil {
		return nil, fmt.Errorf("opening gate log %s: %w", path, err)
	}

	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("closing gate log %s: %w", path, err)
	}

	return &Log{path: path}, nil
}

// Append writes one entry to the log.
func (l *Log) Append(entry LogEntry) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	f, err := os.OpenFile(l.path, os.O_APPEND|os.O_WRONLY, logFilePerm)
	if err != nil {
		return fmt.Errorf("opening gate log %s: %w", l.path, err)
	}
	defer func() { _ = f.Close() }() //nolint:errcheck // append-only write already flushed by Encode below

	if err := json.NewEncoder(f).Encode(entry); err != nil {
		return fmt.Errorf("appending gate log entry: %w", err)
	}

	return nil
}

// Entries reads every recorded entry in order.
func (l *Log) Entries() ([]LogEntry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	f, err := os.Open(l.path)
	if err != nil {
		return nil, fmt.Errorf("opening gate log %s: %w", l.path, err)
	}
	defer func() { _ = f.Close() }() //nolint:errcheck // read-only handle, nothing actionable on close failure

	var entries []LogEntry

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e LogEntry
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			return nil, fmt.Errorf("parsing gate log entry: %w", err)
		}

		entries = append(entries, e)
	}

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading gate log %s: %w", l.path, err)
	}

	return entries, nil
}
