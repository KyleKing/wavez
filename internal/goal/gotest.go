package goal

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// testEvent is one line of `go test -json`: a test2json event, or a build
// event, which carries ImportPath in place of Package.
//
//nolint:tagliatelle // the keys are the go command's
type testEvent struct {
	Action      string `json:"Action"`
	Test        string `json:"Test"`
	Output      string `json:"Output"`
	FailedBuild string `json:"FailedBuild"`
}

// testRun is what the events of one `go test -json` add up to.
type testRun struct {
	// printed holds the first line each test still running has printed.
	printed map[string]string
	// firstFailed is the first test reported as failed, and failure is the
	// first line it printed that is not the runner's own framing.
	firstFailed string
	failure     string
	// build is the first line a failed build printed.
	build string
	// said is the first line a package printed outside any test, and plain
	// is the first line that was not an event at all: the go command's own
	// complaint, on stderr.
	said    string
	plain   string
	passed  int
	skipped int
	// buildFailed says the test binary did not compile. A setupFailed run
	// never got that far: the package could not be loaded at all.
	buildFailed bool
	setupFailed bool
	pkgFailed   bool
}

// runTest runs `go test` for one condition. Exit 0 is not enough to call it
// done, because go exits 0 when -run matches no test, so the events are
// read and at least one test has to have passed.
func runTest(ctx context.Context, root string, item Item) Result {
	args := append([]string{"test", "-json"}, item.Packages...)
	if item.Run != "" {
		args = append(args, "-run", item.Run)
	}

	out := run(ctx, root, item.Timeout, "go", args...)
	result := Result{Name: item.Name, Kind: item.Kind}

	if out.reason != "" {
		result.Status, result.Detail = StatusCouldNotCheck, out.reason

		return result
	}

	seen := readTestRun(out.output)

	result.Status, result.Detail = seen.verdict(out.exit, item)
	result.Detail = clip(result.Detail, detailLimit)

	return result
}

func readTestRun(output string) testRun {
	seen := testRun{printed: map[string]string{}}

	for line := range strings.SplitSeq(output, "\n") {
		var event testEvent

		if !strings.HasPrefix(line, "{") || json.Unmarshal([]byte(line), &event) != nil {
			if seen.plain == "" {
				seen.plain = oneLine(line, "")
			}

			continue
		}

		seen.add(event)
	}

	return seen
}

func (t *testRun) add(event testEvent) {
	text := strings.TrimSpace(event.Output)

	switch event.Action {
	case "build-output":
		if t.build == "" && !strings.HasPrefix(text, "#") {
			t.build = text
		}
	case "output":
		t.addOutput(event.Test, text)
	case "pass":
		if event.Test != "" {
			t.passed++
		}
	case "skip":
		if event.Test != "" {
			t.skipped++
		}
	case "fail":
		t.addFailure(event)
	}
}

func (t *testRun) addOutput(test, text string) {
	framing := text == "" || strings.HasPrefix(text, "=== ") || strings.HasPrefix(text, "--- ")

	switch {
	case strings.Contains(text, "[setup failed]"):
		t.setupFailed = true
	case framing || text == "FAIL" || strings.HasPrefix(text, "FAIL\t"):
	case test == "" && t.said == "":
		t.said = text
	case test != "" && t.printed[test] == "":
		t.printed[test] = text
	}
}

func (t *testRun) addFailure(event testEvent) {
	switch {
	case event.Test == "":
		t.pkgFailed = true
		t.buildFailed = t.buildFailed || event.FailedBuild != ""
	case t.firstFailed == "":
		t.firstFailed, t.failure = event.Test, t.printed[event.Test]
	}
}

// verdict turns a run into a status and its one line of detail. A package
// go could not load, or a go command that failed before any test, is the
// toolchain's failure and not the goal's.
func (t *testRun) verdict(exit int, item Item) (Status, string) {
	switch {
	case t.firstFailed != "" && t.failure != "":
		return StatusNotDone, t.firstFailed + " failed: " + t.failure
	case t.firstFailed != "":
		return StatusNotDone, t.firstFailed + " failed"
	case t.setupFailed:
		return StatusCouldNotCheck, firstOf(t.build, t.plain, "go could not load the package")
	case t.buildFailed:
		return StatusNotDone, "does not build: " + firstOf(t.build, t.plain, "go printed no diagnostic")
	case t.pkgFailed:
		return StatusNotDone, firstOf(t.said, t.plain, "the package's tests failed")
	case exit != 0:
		return StatusCouldNotCheck, firstOf(t.plain, t.said, fmt.Sprintf("go test exited %d without a result", exit))
	case t.passed > 0:
		return StatusDone, ""
	case t.skipped > 0:
		return StatusNotDone, fmt.Sprintf("no test passed: %d skipped", t.skipped)
	case item.Run != "":
		return StatusNotDone, "no test matched -run " + item.Run
	default:
		return StatusNotDone, "no test ran in " + strings.Join(item.Packages, " ")
	}
}

func firstOf(candidates ...string) string {
	for _, candidate := range candidates {
		if candidate != "" {
			return candidate
		}
	}

	return ""
}
