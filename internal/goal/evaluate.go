package goal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"
)

// Status is what checking one condition established.
type Status string

// The statuses a condition can have. CouldNotCheck is an abstention: the
// check never gave an answer, so it is not a failure. Unknown is an
// expensive condition nobody has run against the tree as it is now.
const (
	StatusDone          Status = "done"
	StatusNotDone       Status = "not done"
	StatusCouldNotCheck Status = "could not check"
	StatusUnknown       Status = "unknown"
)

// detailUnknown is the detail of a condition that waits for an expensive
// evaluation.
const detailUnknown = "runs at end of turn"

// Result is one condition's status. Detail is one bounded line saying why a
// condition is not done or could not be checked, and empty when it is done.
type Result struct {
	Name   string `json:"name"`
	Kind   Kind   `json:"kind"`
	Status Status `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// Options says how much an evaluation may spend.
type Options struct {
	// Expensive runs the test and command conditions. Without it they
	// report the verdict cached for the tree as it is now, or unknown.
	Expensive bool
}

// Report is every condition of one goal, in file order.
type Report struct {
	Goal    string   `json:"goal"`
	Results []Result `json:"items"`
	Block   bool     `json:"block"`
}

// Evaluate checks every condition of g against the project at root. The
// file, grep, and absent kinds always run. With opts.Expensive the test and
// command kinds run too and their verdicts are cached; without it they are
// answered from that cache while the tree is unchanged. The report is
// complete even when the error says the cache could not be written.
func Evaluate(ctx context.Context, root string, g Goal, opts Options) (Report, error) {
	report := Report{Goal: g.Sentence, Block: g.Block, Results: make([]Result, 0, len(g.Items))}

	files := openReader(root)
	defer files.close()

	var (
		cached state
		tree   string
	)

	if hasExpensive(g) {
		cached = readState(root)
		tree = treeFingerprint(ctx, root)
	}

	next := state{Items: map[string]entry{}}

	for index := range g.Items {
		item := g.Items[index]

		var result Result

		switch {
		case !item.Kind.Expensive():
			result = files.check(item)
		case opts.Expensive:
			result = runItem(ctx, root, item)
			next.Items[item.Name] = entry{
				EvaluatedAt: time.Now().UTC(), Verdict: result.Status, Detail: result.Detail,
				Definition: item.definition(), Tree: tree,
			}
		default:
			result = cached.lookup(item, tree)
		}

		report.Results = append(report.Results, result)
	}

	if len(next.Items) == 0 || ctx.Err() != nil {
		return report, nil
	}

	if err := writeState(root, next); err != nil {
		return report, fmt.Errorf("caching the goal's verdicts: %w", err)
	}

	return report, nil
}

func hasExpensive(g Goal) bool {
	for index := range g.Items {
		if g.Items[index].Kind.Expensive() {
			return true
		}
	}

	return false
}

// runItem runs one test or command condition.
func runItem(ctx context.Context, root string, item Item) Result {
	if item.Kind == KindTest {
		return runTest(ctx, root, item)
	}

	result := Result{Name: item.Name, Kind: item.Kind}
	out := run(ctx, root, item.Timeout, "sh", "-c", item.Command)

	switch {
	case out.reason != "":
		result.Status, result.Detail = StatusCouldNotCheck, out.reason
	case out.exit == 0:
		result.Status = StatusDone
	case out.exit == exitNotFound || out.exit == exitNotExecutable:
		// The shell's own status for a program it could not find or start.
		result.Status = StatusCouldNotCheck
		result.Detail = oneLine(out.output, fmt.Sprintf("the shell could not run it (exit %d)", out.exit))
	default:
		result.Status = StatusNotDone
		result.Detail = oneLine(out.output, fmt.Sprintf("exit status %d", out.exit))
	}

	return result
}

// reader checks the kinds that only read files. Everything goes through an
// os.Root, so a symlink cannot lead a goal outside the project.
type reader struct {
	root *os.Root
	err  error
}

func openReader(root string) *reader {
	opened, err := os.OpenRoot(root)

	return &reader{root: opened, err: err}
}

func (r *reader) close() {
	if r.root == nil {
		return
	}

	if err := r.root.Close(); err != nil {
		return
	}
}

func (r *reader) check(item Item) Result {
	result := Result{Name: item.Name, Kind: item.Kind}

	if r.err != nil {
		result.Status, result.Detail = StatusCouldNotCheck, oneLine(r.err.Error(), "the project could not be opened")

		return result
	}

	var (
		why string
		err error
	)

	switch item.Kind {
	case KindFile:
		why, err = r.missing(item)
	case KindGrep:
		why, err = r.grep(item)
	case KindAbsent:
		why, err = r.absent(item)
	case KindTest, KindCommand:
		result.Status, result.Detail = StatusUnknown, detailUnknown

		return result
	}

	switch {
	case err != nil:
		result.Status, result.Detail = StatusCouldNotCheck, oneLine(err.Error(), "it could not be read")
	case why != "":
		result.Status, result.Detail = StatusNotDone, clip(why, detailLimit)
	default:
		result.Status = StatusDone
	}

	return result
}

// missing returns why the item is not done, or nothing when its path exists.
func (r *reader) missing(item Item) (string, error) {
	_, err := r.root.Lstat(item.File)
	if errors.Is(err, fs.ErrNotExist) {
		return item.File + " does not exist", nil
	}

	return "", err //nolint:wrapcheck // the caller turns it into one line of detail
}

// grep returns why the item is not done, or nothing when its pattern is in
// its file.
func (r *reader) grep(item Item) (string, error) {
	data, err := r.root.ReadFile(item.File)
	if errors.Is(err, fs.ErrNotExist) {
		return item.File + " does not exist", nil
	}

	if err != nil {
		return "", err //nolint:wrapcheck // the caller turns it into one line of detail
	}

	if item.Pattern.Match(data) {
		return "", nil
	}

	return fmt.Sprintf("no match for /%s/ in %s", item.Pattern, item.File), nil
}

// absent returns the first place the item's pattern is found in a file its
// glob selects, or nothing when it is in none.
func (r *reader) absent(item Item) (string, error) {
	found := ""
	tree := r.root.FS()

	err := fs.WalkDir(tree, globBase(item.Glob.String()), func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}

			return err
		}

		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == ".jj" || entry.Name() == Dir {
				return fs.SkipDir
			}

			return nil
		}

		if !entry.Type().IsRegular() || !item.Glob.Match(name) {
			return nil
		}

		data, err := fs.ReadFile(tree, name)
		if err != nil {
			return err //nolint:wrapcheck // the caller turns it into one line of detail
		}

		if at := item.Pattern.FindIndex(data); at != nil {
			found = fmt.Sprintf("%s:%d matches /%s/", name, 1+bytes.Count(data[:at[0]], []byte("\n")), item.Pattern)

			return fs.SkipAll
		}

		return nil
	})

	return found, err //nolint:wrapcheck // the caller turns it into one line of detail
}

// globBase returns the deepest directory every match of pattern is under,
// so an absent walks that and not the whole project.
func globBase(pattern string) string {
	segments := strings.Split(strings.TrimPrefix(pattern, "./"), "/")
	fixed := 0

	for fixed < len(segments)-1 && !strings.ContainsAny(segments[fixed], "*?[") {
		fixed++
	}

	if fixed == 0 {
		return "."
	}

	return strings.Join(segments[:fixed], "/")
}
