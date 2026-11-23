package reduce_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kyleking/wavez/internal/reduce"
)

const projectRoot = "/home/dev/proj"

// hugeRun is a non-verbose run in which one test printed n lines.
func hugeRun(n int) string {
	var b strings.Builder

	b.WriteString("--- FAIL: TestHuge (0.31s)\n")

	for i := range n {
		fmt.Fprintf(&b, "    huge_test.go:9: line %d of the same complaint, with enough text to fill a row\n", i)
	}

	b.WriteString(closed + failed + "example.com/m/huge\t0.412s\n?   \texample.com/m/cmd\t[no test files]\n")
	b.WriteString("ok  \texample.com/m/fine\t0.002s\n" + closed)

	return b.String()
}

// panicDump is what a test that panics leaves behind: the message, then
// every goroutine, most of them the runtime's.
const panicDump = "--- FAIL: TestBoom (0.00s)\n" +
	"panic: runtime error: index out of range [3] with length 0 [recovered]\n" +
	"\tpanic: runtime error: index out of range [3] with length 0\n" +
	"\n" +
	"goroutine 7 [running]:\n" +
	"testing.tRunner.func1.2({0x52a3c0, 0xc000014120})\n" +
	"\t/usr/local/go/src/testing/testing.go:1734 +0x21c\n" +
	"panic({0x52a3c0?, 0xc000014120?})\n" +
	"\t/usr/local/go/src/runtime/panic.go:787 +0x132\n" +
	"example.com/m/calc.pick(...)\n" +
	"\t" + projectRoot + "/calc/calc.go:12\n" +
	"example.com/m/calc.TestBoom(0xc000003880)\n" +
	"\t" + projectRoot + "/calc/calc_test.go:9 +0x1c\n" +
	"testing.tRunner(0xc000003880, 0x5552a8)\n" +
	"\t/usr/local/go/src/testing/testing.go:1792 +0xf4\n" +
	"created by testing.(*T).Run in goroutine 1\n" +
	"\t/usr/local/go/src/testing/testing.go:1851 +0x413\n" +
	"FAIL\texample.com/m/calc\t0.006s\n" +
	"FAIL\n"

// timeoutDump is what `go test -timeout` prints for a test that hangs.
const timeoutDump = "panic: test timed out after 1m0s\n" +
	"\trunning tests:\n" +
	"\t\tTestHang (1m0s)\n" +
	"\n" +
	"goroutine 17 [running]:\n" +
	"testing.(*M).startAlarm.func1()\n" +
	"\t/usr/local/go/src/testing/testing.go:2484 +0x394\n" +
	"\n" +
	"goroutine 6 [select (no cases)]:\n" +
	"example.com/m/calc.TestHang(0xc000003c00?)\n" +
	"\t" + projectRoot + "/calc/calc_test.go:5 +0xf\n" +
	"testing.tRunner(0xc000003c00, 0x584388)\n" +
	"\t/usr/local/go/src/testing/testing.go:1792 +0xf4\n" +
	"FAIL\texample.com/m/calc\t60.005s\n" +
	"FAIL\n"

func TestFailure(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		in    string
		holds []string
		gone  []string
		limit int
	}{
		{
			name: "twenty thousand lines from one test keep its name, its first lines, and a count",
			in:   hugeRun(20000), limit: 3000,
			holds: []string{
				"--- FAIL: TestHuge (0.31s)\n", "line 0 of", "line 4 of",
				"FAIL\texample.com/m/huge\t0.412s", "(19995 more lines not shown)",
			},
			gone: []string{"line 5 of", "[no test files]", "ok  \t", "bytes cut"},
		},
		{
			name: "a panic keeps its message and the frames inside the project",
			in:   panicDump, limit: 3000,
			holds: []string{
				"--- FAIL: TestBoom", "panic: runtime error: index out of range [3] with length 0 [recovered]",
				"\tat example.com/m/calc.pick (calc/calc.go:12)",
				"\tat example.com/m/calc.TestBoom (calc/calc_test.go:9)",
				"FAIL\texample.com/m/calc\t0.006s",
			},
			gone: []string{projectRoot, "/usr/local/go", "goroutine 7", "testing.tRunner", "+0x"},
		},
		{
			name: "a test that hung past the timeout is named with where it was",
			in:   timeoutDump, limit: 3000,
			holds: []string{
				"panic: test timed out after 1m0s", "TestHang (1m0s)",
				"\tat example.com/m/calc.TestHang (calc/calc_test.go:5)", "FAIL\texample.com/m/calc",
			},
			gone: []string{projectRoot, "startAlarm", "goroutine"},
		},
		{
			name: "a hundred failing tests past the limit all keep their names",
			in: strings.Repeat("--- FAIL: TestOne (0.00s)\n    a_test.go:3: "+strings.Repeat("x", 200)+"\n", 100) +
				closed + failed + "example.com/m\t0.1s\n" + closed,
			limit: 3200,
			holds: []string{
				strings.Repeat("--- FAIL: TestOne (0.00s)\n", 3), failed + "example.com/m\t0.1s",
				"more lines not shown)",
			},
		},
		{
			name: "one enormous line is shortened and says so",
			in:   "check.sh: " + strings.Repeat("y", 5000) + "\nexit status 1\n", limit: 1000,
			holds: []string{"check.sh: yyy", "[line shortened]", "exit status 1"},
		},
		{
			name: "output that fits is left alone apart from its paths",
			in:   projectRoot + "/a.go:3:1: undefined: x\n", limit: 3000,
			holds: []string{"a.go:3:1: undefined: x"},
			gone:  []string{projectRoot, "not shown"},
		},
		{name: "nothing in, nothing out", limit: 3000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := reduce.Failure(tt.in, projectRoot, tt.limit)

			const trailer = 40

			assert.LessOrEqual(t, len(got), tt.limit+trailer)

			for _, want := range tt.holds {
				assert.Contains(t, got+"\n", want)
			}

			for _, not := range tt.gone {
				assert.NotContains(t, got, not)
			}

			for line := range strings.SplitSeq(got, "\n") {
				if got == "" {
					break
				}

				assert.NotEmpty(t, strings.TrimSpace(line), "a blank or cut line in:\n%s", got)
			}

			assert.Equal(t, got, reduce.Output(got), "a second reduction changes nothing")
		})
	}
}

func TestGoTestFailures_TimedOutTestIsNamed(t *testing.T) {
	t.Parallel()

	got := reduce.GoTestFailures(timeoutDump)

	assert.Equal(t, []reduce.PackageFailure{{
		Package: "example.com/m/calc", Tests: []string{"TestHang"}, TimedOut: []string{"TestHang"},
	}}, got)

	without := reduce.WithoutGoTests(timeoutDump, func(_, test string) bool { return test == "TestHang" })
	assert.NotContains(t, without, "timed out")
	assert.NotContains(t, without, "FAIL")
}
