// Package corpus defines the shared Candidate schema and JSON-lines storage
// used by every import source and the review step.
package corpus

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"
)

type Author string

const (
	AuthorMe   Author = "me"
	AuthorTeam Author = "team"
)

// Candidate is one writing sample considered for inclusion in the voice corpus.
type Candidate struct {
	ID        string    `json:"id"`
	Source    string    `json:"source"`
	Author    Author    `json:"author"`
	Context   string    `json:"context"`
	Timestamp time.Time `json:"timestamp"`
	Redacted  bool      `json:"redacted"`
	Text      string    `json:"text"`
	Tags      []string  `json:"tags,omitempty"`
}

// Append writes candidates to the JSON-lines file at path, creating it if
// necessary and preserving any existing contents.
func Append(path string, candidates []Candidate) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("opening corpus file %q: %w", path, err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	for _, c := range candidates {
		if err := enc.Encode(c); err != nil {
			return fmt.Errorf("encoding candidate %q: %w", c.ID, err)
		}
	}
	return nil
}

// Read loads every candidate from a JSON-lines file at path.
func Read(path string) ([]Candidate, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening corpus file %q: %w", path, err)
	}
	defer f.Close()
	return ReadFrom(f)
}

// ReadFrom decodes JSON-lines candidates from an arbitrary reader.
func ReadFrom(r io.Reader) ([]Candidate, error) {
	var out []Candidate
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var c Candidate
		if err := json.Unmarshal(line, &c); err != nil {
			return nil, fmt.Errorf("decoding candidate line: %w", err)
		}
		out = append(out, c)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning corpus file: %w", err)
	}
	return out, nil
}
