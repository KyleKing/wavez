package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kyleking/wavez/internal/app"
	"github.com/kyleking/wavez/internal/bench"
)

// errNoThreadLog reports a -stats argument that names no readable log.
var errNoThreadLog = errors.New("no thread log for that id")

// statsReport prints what one finished run spent. The argument is a thread
// id, or a path to a log file for a run whose project this is not. JSON
// output writes the same fields as one object a script can compare runs by.
//
// A non-empty baseline names a second run the same way and appends one line
// per counter showing that run's value, this run's, and the signed change,
// which is how DESIGN.md's before-and-after objective is read off two logs.
func statsReport(root, arg, baseline string, jsonOut bool) error {
	stats, err := summarizeLog(root, arg)
	if err != nil {
		return err
	}

	if jsonOut {
		return stats.RenderJSON(os.Stdout) //nolint:wrapcheck // bench.Read already names the file and the failure
	}

	if err := stats.Render(os.Stdout); err != nil {
		return err //nolint:wrapcheck // Render's error already names the writer
	}

	if baseline == "" {
		return nil
	}

	before, err := summarizeLog(root, baseline)
	if err != nil {
		return fmt.Errorf("resolving -stats-vs %s: %w", baseline, err)
	}

	return bench.Compare(before, stats, os.Stdout) //nolint:wrapcheck // Compare's error already names the writer
}

// summarizeLog resolves a thread id or log path to the counts for that run.
func summarizeLog(root, arg string) (bench.Stats, error) {
	path := arg
	if !strings.HasSuffix(arg, ".jsonl") {
		path = filepath.Join(app.ThreadLogDir(root), arg+".jsonl")
	}

	if _, err := os.Stat(path); err != nil {
		return bench.Stats{}, fmt.Errorf("%w: %s", errNoThreadLog, path)
	}

	events, err := bench.Read(path)
	if err != nil {
		return bench.Stats{}, err //nolint:wrapcheck // bench.Read already names the file and the failure
	}

	return bench.Summarize(events), nil
}
