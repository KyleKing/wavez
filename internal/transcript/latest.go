package transcript

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// headLines is how many lines of a transcript Latest reads looking for the
// directory the session ran in.
const headLines = 40

// Latest returns the most recently written transcript under root of a
// session that ran in dir or below it. It reads where each session ran from
// the transcript itself rather than from the folder name, for the reason
// Find gives.
func Latest(root, dir string) (string, error) {
	paths, err := filepath.Glob(filepath.Join(root, "*", "*"+extension))
	if err != nil {
		return "", fmt.Errorf("searching %s: %w", root, err)
	}

	var (
		best   string
		bestAt time.Time
	)

	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || !info.ModTime().After(bestAt) {
			continue
		}

		if within(dir, ranIn(path)) {
			best, bestAt = path, info.ModTime()
		}
	}

	if best == "" {
		return "", &NotFoundError{Root: root, Session: "any session in " + dir}
	}

	return best, nil
}

// ranIn returns the working directory a transcript's first lines name, or
// "" when none of them does.
func ranIn(path string) string {
	f, err := os.Open(path) //nolint:gosec // a transcript found under the root the caller named
	if err != nil {
		return ""
	}

	defer func() {
		// The file was only read, so a failed close loses nothing.
		if err := f.Close(); err != nil {
			return
		}
	}()

	br := bufio.NewReader(f)

	for range headLines {
		line, err := br.ReadBytes('\n')

		var e struct {
			Cwd string `json:"cwd"`
		}

		if json.Unmarshal(line, &e) == nil && e.Cwd != "" {
			return e.Cwd
		}

		if err != nil {
			if !errors.Is(err, io.EOF) {
				return ""
			}

			break
		}
	}

	return ""
}

// within reports whether path is dir or lies below it.
func within(dir, path string) bool {
	if path == "" {
		return false
	}

	rel, err := filepath.Rel(dir, path)

	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
