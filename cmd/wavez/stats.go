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
// id, or a path to a log file for a run whose project this is not.
func statsReport(root, arg string) error {
	path := arg
	if !strings.HasSuffix(arg, ".jsonl") {
		path = filepath.Join(app.ThreadLogDir(root), arg+".jsonl")
	}

	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("%w: %s", errNoThreadLog, path)
	}

	events, err := bench.Read(path)
	if err != nil {
		return err //nolint:wrapcheck // bench.Read already names the file and the failure
	}

	return bench.Summarize(events).Render(os.Stdout) //nolint:wrapcheck // same
}
