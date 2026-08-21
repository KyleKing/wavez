// Package replay runs a fixed task set through the loop and keeps one record
// per run. DESIGN.md's before-and-after objective needs two runs of the same
// task, and a dogfood pair of two different tasks of similar size cannot
// separate what a lane changed from what the task cost.
package replay

import (
	"bufio"
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

// Task is one line of the set: a stable id, and the prompt every run of it
// is given word for word.
type Task struct {
	ID     string
	Prompt string
}

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

		id, prompt, ok := strings.Cut(text, "|")
		id = strings.TrimSpace(id)
		if !ok || id == "" || strings.TrimSpace(prompt) == "" {
			return nil, fmt.Errorf("%s line %d: %w", path, line, ErrMalformedTask)
		}

		if _, dup := seen[id]; dup {
			return nil, fmt.Errorf("%s line %d: %w: %q", path, line, ErrDuplicateTask, id)
		}

		seen[id] = struct{}{}
		out = append(out, Task{ID: id, Prompt: strings.TrimSpace(prompt)})
	}

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading task set %s: %w", path, err)
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
