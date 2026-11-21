package bench

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// CheckKind is what a check looks at.
type CheckKind string

// The check kinds. A file check has no keyword: it is spelled
// `path:substring`.
const (
	CheckFile   CheckKind = "file"
	CheckAnswer CheckKind = "answer"
	CheckBuild  CheckKind = "build"
	CheckTest   CheckKind = "test"
	CheckCmd    CheckKind = "cmd"
)

// Check is one thing a finished run must satisfy.
type Check struct {
	Kind CheckKind
	// Path is the file a file check reads, relative to the workspace.
	Path string
	// Want is the substring a file or answer check looks for, the package
	// pattern of a build or test check, or the shell command of a cmd
	// check.
	Want string
	// Negate inverts the check, so it holds when the plain form would not.
	Negate bool
}

// Errors a check string can fail to parse with.
var (
	errEmptyCheck   = errors.New("empty check")
	errNoSeparator  = errors.New("want kind:value or path:substring")
	errEmptyOperand = errors.New("nothing after the colon")
	errEmptyPath    = errors.New("no path before the colon")
)

// keywords are the prefixes that name a kind; anything else before the
// first colon is a path.
var keywords = []CheckKind{CheckAnswer, CheckBuild, CheckTest, CheckCmd}

// ParseCheck reads one check: `path:substring`, `answer:substring`,
// `build:<go pattern>`, `test:<go pattern>`, or `cmd:<shell command>`, with
// a leading `!` to invert it.
func ParseCheck(text string) (Check, error) {
	var c Check

	body, negated := strings.CutPrefix(text, "!")
	c.Negate = negated

	if strings.TrimSpace(body) == "" {
		return Check{}, errEmptyCheck
	}

	head, rest, ok := strings.Cut(body, ":")
	if !ok {
		return Check{}, errNoSeparator
	}

	if rest == "" {
		return Check{}, errEmptyOperand
	}

	c.Kind, c.Want = CheckFile, rest

	for _, k := range keywords {
		if head == string(k) {
			c.Kind = k

			return c, nil
		}
	}

	if head == "" {
		return Check{}, errEmptyPath
	}

	c.Path = head

	return c, nil
}

// String is the check as a tasks file spells it.
func (c Check) String() string {
	head := string(c.Kind)
	if c.Kind == CheckFile {
		head = c.Path
	}

	bang := ""
	if c.Negate {
		bang = "!"
	}

	return bang + head + ":" + c.Want
}

// CheckResult is one check and whether the run satisfied it.
type CheckResult struct {
	Check string `json:"check"`
	// Detail is one line on why a failed check failed.
	Detail string `json:"detail,omitempty"`
	Pass   bool   `json:"pass"`
}

// Verify evaluates checks against the tree a run left at dir and the answer
// it finished with. A file check naming a file that does not exist fails
// rather than erroring, since a run that never created the file is exactly
// what the check is there to catch.
func Verify(ctx context.Context, checks []Check, dir, answer string) []CheckResult {
	out := make([]CheckResult, 0, len(checks))

	for _, c := range checks {
		held, detail := c.evaluate(ctx, dir, answer)
		res := CheckResult{Check: c.String(), Pass: held != c.Negate}

		switch {
		case res.Pass:
		case c.Negate:
			res.Detail = "holds, and the check says it must not"
		case detail == "":
			res.Detail = "not satisfied"
		default:
			res.Detail = detail
		}

		out = append(out, res)
	}

	return out
}

// Passed reports whether every check held. No checks at all is not a pass:
// a run that was never checked proved nothing.
func Passed(results []CheckResult) bool {
	for _, r := range results {
		if !r.Pass {
			return false
		}
	}

	return len(results) > 0
}

func (c Check) evaluate(ctx context.Context, dir, answer string) (bool, string) {
	switch c.Kind {
	case CheckAnswer:
		return strings.Contains(answer, c.Want), ""
	case CheckBuild, CheckTest:
		return command(ctx, dir, "go", string(c.Kind), c.Want)
	case CheckCmd:
		return command(ctx, dir, "sh", "-c", c.Want)
	case CheckFile:
		return fileContains(filepath.Join(dir, c.Path), c.Want)
	default:
		return false, "unknown check kind " + string(c.Kind)
	}
}

func fileContains(path, want string) (bool, string) {
	body, err := os.ReadFile(path) //nolint:gosec // the path comes from the tasks file
	if err != nil {
		return false, firstLine(err.Error())
	}

	return strings.Contains(string(body), want), ""
}

// command runs a check's program in the workspace and passes on exit 0.
func command(ctx context.Context, dir, name string, args ...string) (bool, string) {
	out, err := run(ctx, invocation{Dir: dir, Name: name, Args: args})
	if err == nil {
		return true, ""
	}

	if line := firstLine(string(out)); line != "" {
		return false, line
	}

	return false, firstLine(err.Error())
}

// maxDetail bounds one check's detail, so a compiler line cannot swamp a
// record.
const maxDetail = 200

// firstLine is the first line of output that says something. `go build`
// heads its output with `# package`, which names nothing a reader needs.
func firstLine(out string) string {
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if len(line) > maxDetail {
			line = strings.ToValidUTF8(line[:maxDetail], "") + "…"
		}

		return line
	}

	return ""
}
