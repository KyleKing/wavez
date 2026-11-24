package bench

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/kyleking/wavez/internal/config"
)

// Task is one pinned piece of work: a prompt, the tree it runs against, and
// the checks that say whether a run did it.
type Task struct {
	ID     string
	Prompt string
	// Repo is a local path or a clone URL. A relative path was resolved
	// against the tasks file when the task was loaded.
	Repo string
	// Rev is the commit the workspace is checked out at, so a task never
	// drifts with the tree it was written against.
	Rev string
	// source is Repo as the tasks file spelled it, which is what the hash
	// covers so the same file hashes alike on every machine.
	source string
	// Gates is the [[check]] tables, as TOML, that a wired arm adds to the
	// workspace's .wavez.toml. The tasks file declares them once as
	// [[gate]] for every task in it, so a project in a language the
	// built-in gates do not know is still gated. Empty when it declares
	// none.
	Gates  string
	Checks []Check
}

// ValidationError names the task and key a tasks file got wrong.
type ValidationError struct {
	Task   string
	Key    string
	Reason string
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("task %q: %s: %s", e.Task, e.Key, e.Reason)
}

// UnknownTaskError says a task id is not in the set.
type UnknownTaskError struct {
	ID string
}

func (e *UnknownTaskError) Error() string { return fmt.Sprintf("no task with id %q", e.ID) }

// hashLength is how many hex digits of the task hash a record keeps.
const hashLength = 12

// hashedFields is how many fields besides the checks go into the hash.
const hashedFields = 4

// Hash identifies the task's definition, so two records are known to have
// answered the same question.
func (t *Task) Hash() string {
	parts := make([]string, 0, hashedFields+len(t.Checks))
	parts = append(parts, t.ID, t.Prompt, t.source, t.Rev)
	for _, c := range t.Checks {
		parts = append(parts, c.String())
	}

	// Only a task with gates hashes them, so every task written before
	// gates existed keeps the hash its records carry.
	if t.Gates != "" {
		parts = append(parts, t.Gates)
	}

	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))

	return hex.EncodeToString(sum[:])[:hashLength]
}

// rawTask is a task as the file spells it.
type rawTask struct {
	ID     string   `toml:"id"`
	Prompt string   `toml:"prompt"`
	Repo   string   `toml:"repo"`
	Rev    string   `toml:"rev"`
	Checks []string `toml:"checks"`
}

// revPattern accepts an abbreviated or full commit hash and nothing that
// could move, such as a branch or a tag.
var revPattern = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// LoadTasks reads and validates a tasks file. A task with no repo runs
// against the repository the file lives in.
func LoadTasks(path string) ([]Task, error) {
	var file struct {
		Task []rawTask        `toml:"task"`
		Gate []map[string]any `toml:"gate"`
	}

	meta, err := toml.DecodeFile(path, &file)
	if err != nil {
		return nil, fmt.Errorf("reading tasks: %w", err)
	}

	if extra := meta.Undecoded(); len(extra) > 0 {
		return nil, &ValidationError{Task: "", Key: extra[0].String(), Reason: "unknown key"}
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolving %s: %w", path, err)
	}

	gates, err := renderGates(file.Gate)
	if err != nil {
		return nil, err
	}

	tasks := make([]Task, 0, len(file.Task))
	seen := map[string]bool{}

	for i := range file.Task {
		task, err := parseTask(&file.Task[i], filepath.Dir(abs))
		if err != nil {
			return nil, err
		}

		if seen[task.ID] {
			return nil, &ValidationError{Task: task.ID, Key: "id", Reason: "defined twice"}
		}

		seen[task.ID] = true
		task.Gates = gates

		tasks = append(tasks, task)
	}

	return tasks, nil
}

func parseTask(raw *rawTask, dir string) (Task, error) {
	fail := func(key, reason string) (Task, error) {
		return Task{}, &ValidationError{Task: raw.ID, Key: key, Reason: reason}
	}

	switch {
	case strings.TrimSpace(raw.ID) == "":
		return fail("id", "required")
	case strings.TrimSpace(raw.Prompt) == "":
		return fail("prompt", "required")
	case raw.Rev == "":
		return fail("rev", "required, so the task cannot drift with the tree")
	case !revPattern.MatchString(raw.Rev):
		return fail("rev", "must be a commit hash, not a branch or tag")
	}

	task := Task{ID: raw.ID, Prompt: raw.Prompt, Rev: raw.Rev, Repo: resolveRepo(raw.Repo, dir), source: raw.Repo}

	for i, text := range raw.Checks {
		check, err := ParseCheck(text)
		if err != nil {
			return fail(fmt.Sprintf("checks[%d]", i), err.Error())
		}

		task.Checks = append(task.Checks, check)
	}

	return task, nil
}

// renderGates writes the tasks file's [[gate]] tables as the [[check]]
// tables of a .wavez.toml, and refuses any that file would refuse, so a
// typo fails when the tasks load and not in the middle of a run.
func renderGates(gates []map[string]any) (string, error) {
	if len(gates) == 0 {
		return "", nil
	}

	var buf bytes.Buffer

	if err := toml.NewEncoder(&buf).Encode(map[string]any{"check": gates}); err != nil {
		return "", &ValidationError{Task: "", Key: "gate", Reason: err.Error()}
	}

	if _, err := config.Parse("[[gate]]", buf.Bytes()); err != nil {
		return "", &ValidationError{Task: "", Key: "gate", Reason: err.Error()}
	}

	return buf.String(), nil
}

// resolveRepo turns the repo a task names into something git accepts from
// any working directory.
func resolveRepo(repo, dir string) string {
	switch {
	case repo == "":
		return dir
	case isURL(repo) || filepath.IsAbs(repo):
		return repo
	default:
		return filepath.Join(dir, repo)
	}
}

func isURL(repo string) bool {
	return strings.Contains(repo, "://") || strings.HasPrefix(repo, "git@")
}

// FindTask returns the task with the given id.
func FindTask(tasks []Task, id string) (Task, error) {
	for i := range tasks {
		if tasks[i].ID == id {
			return tasks[i], nil
		}
	}

	return Task{}, &UnknownTaskError{ID: id}
}
