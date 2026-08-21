package replay

import (
	"fmt"
	"os"
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
func Verify(task Task, dir, answer string) []CheckResult {
	out := make([]CheckResult, 0, len(task.Checks))

	for _, c := range task.Checks {
		body, note := checkSubject(c, dir, answer)
		held := strings.Contains(body, c.Want)

		res := CheckResult{Check: c.String(), Pass: held != c.Negate, Note: note}
		if !res.Pass && note == "" {
			res.Note = "not satisfied"
		}

		out = append(out, res)
	}

	return out
}

func checkSubject(c Check, dir, answer string) (string, string) { //nolint:gocritic // named returns are forbidden
	if c.Path == AnswerPath {
		return answer, ""
	}

	body, err := os.ReadFile(filepath.Join(dir, c.Path)) //nolint:gosec // the path comes from the task set
	if err != nil {
		return "", fmt.Sprintf("cannot read %s: %v", c.Path, err)
	}

	return string(body), ""
}

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
