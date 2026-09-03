// Package fanout splits a job too wide for one prompt into lanes whose
// write sets do not overlap, so the harness can check the split rather than
// trust it.
//
// Leases fence concurrent writes and scoped gate reports keep one lane's
// failure off another, which is what makes several threads against one tree
// safe. Neither answers who should write what. Fanning out over a list
// nobody proved disjoint is how two lanes edit one file, wait on each
// other's lease, and read each other's failures as their own.
package fanout

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

var (
	// ErrUnscoped is a task naming no path. It cannot be fenced, so it cannot
	// be a lane of its own, and including it would make every lane's
	// disjointness a guess.
	ErrUnscoped = errors.New("fanout: a task names no path")
	// ErrNoLanes is a split asked for fewer than one lane.
	ErrNoLanes = errors.New("fanout: at least one lane is required")
	// ErrOverlap is two lanes that write the same path, which Check names and
	// Split cannot produce.
	ErrOverlap = errors.New("fanout: lanes overlap")
)

// Task is one unit of a wider job: what to do, and every path doing it may
// write. Weight orders the packing and is free to be a finding count, a line
// count, or 1.
type Task struct {
	Name   string
	Paths  []string
	Weight int
}

// Lane is the work one thread takes, and Paths is the union its scope is
// fenced to. No other lane in the same split names a path that overlaps one
// of these.
type Lane struct {
	Tasks  []Task
	Paths  []string
	Weight int
}

// Split partitions tasks into at most n lanes with disjoint path sets.
//
// Two tasks that share a path, or whose paths nest, always land in the same
// lane: the split follows the overlap rather than the requested count, so
// n is a ceiling on lanes and never a promise of them. One group of tasks
// that all touch one file stays one lane however large it is, because the
// alternative is a split the leases would serialize anyway.
func Split(tasks []Task, n int) ([]Lane, error) {
	if n < 1 {
		return nil, fmt.Errorf("%w: %d requested", ErrNoLanes, n)
	}

	clean, err := normalize(tasks)
	if err != nil {
		return nil, err
	}

	if len(clean) == 0 {
		return nil, nil
	}

	return pack(group(clean), n), nil
}

// Check reports whether lanes are disjoint, naming the first pair that is
// not. It exists so a split proposed by something other than Split, a model
// or a hand-written plan, is verified rather than believed.
func Check(lanes []Lane) error {
	for i := range lanes {
		for j := i + 1; j < len(lanes); j++ {
			for _, a := range lanes[i].Paths {
				for _, b := range lanes[j].Paths {
					if overlaps(a, b) {
						return fmt.Errorf("%w: lanes %d and %d both write %s and %s",
							ErrOverlap, i, j, a, b)
					}
				}
			}
		}
	}

	return nil
}

// overlaps reports whether two paths name work that cannot run in parallel.
// A path overlaps itself, and a directory overlaps everything beneath it,
// which is the rule the leases already use: a lane holding a subtree and a
// lane holding one file in it are one lane's work.
func overlaps(a, b string) bool {
	if a == b {
		return true
	}

	return strings.HasPrefix(b, a+string(filepath.Separator)) ||
		strings.HasPrefix(a, b+string(filepath.Separator))
}

// normalize cleans each task's paths and refuses one that names none. A
// weight below 1 counts as 1, so an unweighted split still balances by task
// count rather than putting everything in one lane.
func normalize(tasks []Task) ([]Task, error) {
	out := make([]Task, 0, len(tasks))

	for _, t := range tasks {
		paths := make([]string, 0, len(t.Paths))

		for _, p := range t.Paths {
			if p = filepath.Clean(strings.TrimSpace(p)); p != "" && p != "." {
				paths = append(paths, p)
			}
		}

		if len(paths) == 0 {
			return nil, fmt.Errorf("%w: %q", ErrUnscoped, t.Name)
		}

		sort.Strings(paths)

		if t.Weight < 1 {
			t.Weight = 1
		}

		t.Paths = paths
		out = append(out, t)
	}

	return out, nil
}
