package bench

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/kyleking/wavez/internal/transcript"
)

// Record is one run of one task under one arm, as a line of the records
// file.
type Record struct {
	TaskID string `json:"task_id"`
	// TaskHash identifies the task's definition, so a report can tell two
	// runs of an edited task apart.
	TaskHash string `json:"task_hash"`
	Arm      Arm    `json:"arm"`
	Harness  string `json:"harness"`
	// Model is the model asked for and Served the one that answered.
	Model        string `json:"model"`
	Served       string `json:"served"`
	Rev          string `json:"rev"`
	WavezVersion string `json:"wavez_version"`
	// Started is when the harness was launched, RFC 3339 in UTC.
	Started string `json:"started"`
	Stop    string `json:"stop"`
	// Error is what went wrong outside the model's control: a harness that
	// would not start, a transcript that could not be read.
	Error     string `json:"error,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	// Workspace is where the tree was left, set only when it was kept.
	Workspace string `json:"workspace,omitempty"`
	// Transcript is the attribution of the session the run left, absent
	// when the harness keeps no transcript or it could not be found.
	Transcript *transcript.Stats `json:"transcript,omitempty"`
	Checks     []CheckResult     `json:"checks"`
	Usage      Usage             `json:"usage"`
	Turns      int               `json:"turns"`
	CostUSD    float64           `json:"cost_usd"`
	// WallMS is the wall clock this package measured around the harness;
	// HarnessMS is what the harness reported for itself.
	WallMS    int64 `json:"wall_ms"`
	HarnessMS int64 `json:"harness_ms"`
	// Pass is every check holding on a run the harness completed.
	Pass bool `json:"pass"`
}

// Permissions for the records file and the directory made to hold it.
const (
	recordsFileMode = 0o644
	recordsDirMode  = 0o755
)

// AppendRecord adds one line to the records file, creating it and its
// directory when absent. Earlier lines are never rewritten: the file is
// opened for appending only.
func AppendRecord(path string, rec *Record) error {
	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("encoding record: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), recordsDirMode); err != nil {
		return fmt.Errorf("creating records directory: %w", err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, recordsFileMode) //nolint:gosec // owner's path
	if err != nil {
		return fmt.Errorf("opening records: %w", err)
	}

	_, err = f.Write(append(line, '\n'))
	if err = errors.Join(err, f.Close()); err != nil {
		return fmt.Errorf("appending record: %w", err)
	}

	return nil
}

// LoadRecords reads every record in the file, oldest first. A file that
// does not exist holds no records. A line that does not parse is an error
// naming its number, since a report over a silently shortened history
// would be wrong without saying so.
func LoadRecords(path string) ([]Record, error) {
	f, err := os.Open(path) //nolint:gosec // the owner names the records file
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("opening records: %w", err)
	}

	recs, err := readRecords(f)

	return recs, errors.Join(err, f.Close())
}

func readRecords(r io.Reader) ([]Record, error) {
	var recs []Record

	br := bufio.NewReader(r)

	for n := 1; ; n++ {
		line, err := br.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var rec Record
			if jsonErr := json.Unmarshal(line, &rec); jsonErr != nil {
				return nil, fmt.Errorf("records line %d: %w", n, jsonErr)
			}

			recs = append(recs, rec)
		}

		if errors.Is(err, io.EOF) {
			return recs, nil
		}

		if err != nil {
			return nil, fmt.Errorf("reading records: %w", err)
		}
	}
}
