// Package replay runs a fixed task set through the loop and keeps one record
// per run. DESIGN.md's before-and-after objective needs two runs of the same
// task, and a dogfood pair of two different tasks of similar size cannot
// separate what a lane changed from what the task cost.
package replay

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
)

// DefaultTasksPath is where this repo keeps the fixed set, relative to the
// project root.
const DefaultTasksPath = "_ai_/bench/timing/tasks.txt"

// ErrUnknownTask reports an id the task file does not define.
var ErrUnknownTask = errors.New("no such task")

// ErrMalformedTask reports a line that is not "id|prompt".
var ErrMalformedTask = errors.New("task line is not id|prompt")

// ErrDuplicateTask reports an id the set defines twice, which would make a
// record ambiguous about which prompt produced it.
var ErrDuplicateTask = errors.New("task is defined twice")

// Check.Path values that read something other than a file in the tree.
const (
	// AnswerPath reads the run's final text, for a task whose product is an
	// answer rather than an edit.
	AnswerPath = "answer"
	// BuildPath runs `go build` over the package pattern in Want.
	BuildPath = "build"
	// TestPath runs `go test` over the package pattern in Want.
	TestPath = "test"
)

// Check is one assertion about what a run left behind: Want must appear in
// the file at Path, or must not when Negate. A task with no checks records
// what it spent and proves nothing about what it did.
type Check struct {
	Path   string
	Want   string
	Negate bool
}

// String renders a check the way the task file writes it.
func (c Check) String() string {
	if c.Negate {
		return "!" + c.Path + ":" + c.Want
	}

	return c.Path + ":" + c.Want
}

// Task is one line of the set: a stable id, the prompt every run of it is
// given word for word, and what has to be true afterwards.
type Task struct {
	ID     string
	Prompt string
	Checks []Check
}

// Hash identifies the prompt a run was given, so a report can tell that the
// task text changed between two records of the same id.
func (t Task) Hash() string {
	sum := sha256.Sum256([]byte(t.Prompt))

	return hex.EncodeToString(sum[:])[:hashWidth]
}

const hashWidth = 8

// LoadTasks reads "id|prompt" lines, skipping blank ones and those opening
// with #.
func LoadTasks(path string) ([]Task, error) {
	f, err := os.Open(path) //nolint:gosec // path names a task set the caller chose
	if err != nil {
		return nil, fmt.Errorf("opening task set %s: %w", path, err)
	}
	defer func() { _ = f.Close() }() //nolint:errcheck // read-only handle

	var (
		out  []Task
		seen = map[string]struct{}{}
		line int
	)

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line++

		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}

		fields := strings.Split(text, "|")
		id := strings.TrimSpace(fields[0])
		if len(fields) < 2 || id == "" || strings.TrimSpace(fields[1]) == "" {
			return nil, fmt.Errorf("%s line %d: %w", path, line, ErrMalformedTask)
		}

		if _, dup := seen[id]; dup {
			return nil, fmt.Errorf("%s line %d: %w: %q", path, line, ErrDuplicateTask, id)
		}

		checks, err := parseChecks(fields[2:])
		if err != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, line, err)
		}

		seen[id] = struct{}{}
		out = append(out, Task{ID: id, Prompt: strings.TrimSpace(fields[1]), Checks: checks})
	}

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading task set %s: %w", path, err)
	}

	return out, nil
}

// ErrMalformedCheck reports a check field that is not path:substring.
var ErrMalformedCheck = errors.New("check is not path:substring")

func parseChecks(fields []string) ([]Check, error) {
	var out []Check

	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}

		negate := strings.HasPrefix(f, "!")
		path, want, ok := strings.Cut(strings.TrimPrefix(f, "!"), ":")
		if !ok || strings.TrimSpace(path) == "" || want == "" {
			return nil, fmt.Errorf("%w: %q", ErrMalformedCheck, f)
		}

		out = append(out, Check{Path: strings.TrimSpace(path), Want: want, Negate: negate})
	}

	return out, nil
}

// Find returns the task with the given id.
func Find(tasks []Task, id string) (Task, error) {
	for _, t := range tasks {
		if t.ID == id {
			return t, nil
		}
	}

	ids := make([]string, 0, len(tasks))
	for _, t := range tasks {
		ids = append(ids, t.ID)
	}

	return Task{}, fmt.Errorf("%w: %q (have %s)", ErrUnknownTask, id, strings.Join(ids, ", "))
}
