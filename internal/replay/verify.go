package replay

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CheckResult is one check and whether the run satisfied it.
type CheckResult struct {
	Check string `json:"check"`
	Note  string `json:"note,omitempty"`
	Pass  bool   `json:"pass"`
}

// Verify evaluates a task's checks against the tree the run left at dir and
// the answer it finished with. A check naming a file that does not exist
// fails rather than erroring, since a run that never created the file is
// exactly what the check is there to catch.
func Verify(ctx context.Context, task Task, dir, answer string) []CheckResult {
	out := make([]CheckResult, 0, len(task.Checks))

	for _, c := range task.Checks {
		held, note := evaluate(ctx, c, dir, answer)

		res := CheckResult{Check: c.String(), Pass: held != c.Negate, Note: note}
		if !res.Pass && note == "" {
			res.Note = "not satisfied"
		}

		out = append(out, res)
	}

	return out
}

//nolint:gocritic // named returns are forbidden
func evaluate(ctx context.Context, c Check, dir, answer string) (bool, string) {
	switch c.Path {
	case AnswerPath:
		return strings.Contains(answer, c.Want), ""
	case BuildPath, TestPath:
		return goCommand(ctx, c.Path, dir, c.Want)
	default:
		body, err := os.ReadFile(filepath.Join(dir, c.Path)) //nolint:gosec // the path comes from the task set
		if err != nil {
			return false, fmt.Sprintf("cannot read %s: %v", c.Path, err)
		}

		return strings.Contains(string(body), c.Want), ""
	}
}

// goCommand runs `go build` or `go test` over a package pattern in the
// workspace. A substring check cannot tell an edit that compiles from one
// that does not, and a run whose rename missed a caller in another file
// passed every substring check the task had.
//
//nolint:gocritic // named returns are forbidden
func goCommand(ctx context.Context, name, dir, pattern string) (bool, string) {
	cmd := exec.CommandContext(ctx, "go", name, pattern) //nolint:gosec // both arguments come from the task set
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, ""
	}

	return false, firstLine(string(out))
}

// firstLine is the compiler's first real complaint. `go build` heads its
// output with the package name, which names nothing a reader needs.
func firstLine(out string) string {
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if len(line) > maxNote {
			line = line[:maxNote] + "…"
		}

		return line
	}

	return "no output"
}

// maxNote bounds one check's note, so a compiler line cannot swamp a record.
const maxNote = 200

// Passed reports whether every check held. A task with no checks passes
// vacuously, which Record distinguishes by counting them.
func Passed(results []CheckResult) bool {
	for _, r := range results {
		if !r.Pass {
			return false
		}
	}

	return true
}
