package fanout

import (
	"sort"

	"github.com/kyleking/wavez/internal/reduce"
)

// FromFindings builds one task per file a check reported diagnostics on,
// weighted by how many it found there.
//
// A partition by file is disjoint before anything checks it, which is what
// makes a linter's own output the first work set worth fanning out over: the
// harness does not have to believe a model's claim that two slices do not
// overlap. What it cannot see is a fix in one file that requires a matching
// edit in another, so a run whose findings are that shape has to say so
// rather than be split.
func FromFindings(output string) []Task {
	counts := reduce.Sites(output)

	files := make([]string, 0, len(counts))
	for file := range counts {
		files = append(files, file)
	}

	sort.Strings(files)

	tasks := make([]Task, 0, len(files))
	for _, file := range files {
		tasks = append(tasks, Task{Name: file, Paths: []string{file}, Weight: counts[file]})
	}

	return tasks
}
