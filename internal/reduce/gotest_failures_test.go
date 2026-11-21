package reduce_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kyleking/wavez/internal/reduce"
)

const (
	// The failed prefix opens a failing package's verdict line.
	failed = "FAIL\t"
	// The closed line ends a package's failing tests and a failing run.
	closed = "FAIL\n"
)

// A run over four packages: one with two failing tests, one of which fails
// through a subtest; one that passes; one that does not compile; and one
// that fails without naming a test.
const mixedRun = "# example.com/m/broken [example.com/m/broken.test]\n" +
	"broken/b_test.go:5:2: undefined: nope\n" +
	"--- FAIL: TestOld (0.00s)\n" +
	"    a_test.go:9: old trouble\n" +
	"--- FAIL: TestTable (0.00s)\n" +
	"    --- FAIL: TestTable/first_case (0.00s)\n" +
	"        a_test.go:20: want 1\n" +
	"    --- FAIL: TestTable/second_case (0.00s)\n" +
	"        a_test.go:20: want 2\n" +
	closed + failed + "example.com/m/a\t0.004s\n" +
	"ok  \texample.com/m/fine\t0.002s\n" +
	"FAIL\texample.com/m/broken [build failed]\n" +
	"exit status 3\n" +
	"FAIL\texample.com/m/main\t0.001s\n" +
	"FAIL\n"

func TestGoTestFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want []reduce.PackageFailure
	}{
		{
			name: "each failing package with the tests it named",
			in:   mixedRun,
			want: []reduce.PackageFailure{
				{Package: "example.com/m/a", Tests: []string{
					"TestOld", "TestTable", "TestTable/first_case", "TestTable/second_case",
				}},
				{Package: "example.com/m/broken", NotBuilt: true},
				{Package: "example.com/m/main"},
			},
		},
		{
			name: "a verbose run names the same tests",
			in: "=== RUN   TestX\n=== RUN   TestX/sub\n    x_test.go:3: boom\n" +
				"--- FAIL: TestX (0.00s)\n    --- FAIL: TestX/sub (0.00s)\n" +
				closed + failed + "example.com/m\t0.1s\n",
			want: []reduce.PackageFailure{{Package: "example.com/m", Tests: []string{"TestX", "TestX/sub"}}},
		},
		{
			name: "a package whose dependencies did not load was not built",
			in:   "FAIL\texample.com/m [setup failed]\n",
			want: []reduce.PackageFailure{{Package: "example.com/m", NotBuilt: true}},
		},
		{name: "a passing run has none", in: "ok  \texample.com/m\t0.1s\n"},
		{name: "a failure with no verdict after it belongs to no package", in: "--- FAIL: TestCut (0.00s)\n"},
		{name: "empty output has none"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, reduce.GoTestFailures(tt.in))
		})
	}
}

func TestWithoutGoTests(t *testing.T) {
	t.Parallel()

	named := func(names ...string) func(string, string) bool {
		return func(_, test string) bool {
			for _, name := range names {
				if name == test {
					return true
				}
			}

			return false
		}
	}

	tests := []struct {
		name  string
		in    string
		drop  func(pkg, test string) bool
		holds []string
		gone  []string
	}{
		{
			name:  "a dropped test takes its output with it and leaves its neighbors",
			in:    mixedRun,
			drop:  named("TestOld"),
			holds: []string{"--- FAIL: TestTable (", "want 1", "FAIL\texample.com/m/a\t", "undefined: nope"},
			gone:  []string{"TestOld", "old trouble"},
		},
		{
			name:  "a test with one subtest that stays is kept whole",
			in:    mixedRun,
			drop:  named("TestTable", "TestTable/first_case"),
			holds: []string{"TestTable/first_case", "TestTable/second_case", "TestOld"},
		},
		{
			name: "a package whose every failure is dropped goes with its verdict",
			in:   mixedRun,
			drop: named("TestOld", "TestTable", "TestTable/first_case", "TestTable/second_case"),
			holds: []string{
				"FAIL\texample.com/m/broken [build failed]", "FAIL\texample.com/m/main\t", "exit status 3",
			},
			gone: []string{"example.com/m/a", "TestOld", "want 2"},
		},
		{
			name:  "a package that named no test is never dropped",
			in:    "exit status 3\nFAIL\texample.com/m/main\t0.001s\nFAIL\n",
			drop:  func(string, string) bool { return true },
			holds: []string{"FAIL\texample.com/m/main\t", "FAIL\n"},
		},
		{
			name: "nothing is left of a run whose only failure is dropped",
			in: "--- FAIL: TestOld (0.00s)\n    a_test.go:9: old\n" +
				closed + failed + "example.com/m\t0.004s\nFAIL\n",
			drop: named("TestOld"),
			gone: []string{"FAIL", "old"},
		},
		{
			name: "the package decides, not only the name",
			in: "--- FAIL: TestSame (0.00s)\n" + closed + failed + "example.com/m/a\t0.1s\n" +
				"--- FAIL: TestSame (0.00s)\n" + closed + failed + "example.com/m/b\t0.1s\nFAIL\n",
			drop:  func(pkg, _ string) bool { return pkg == "example.com/m/a" },
			holds: []string{"FAIL\texample.com/m/b\t", "TestSame"},
			gone:  []string{"example.com/m/a"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := reduce.WithoutGoTests(tt.in, tt.drop)

			for _, want := range tt.holds {
				assert.Contains(t, got, want)
			}

			for _, not := range tt.gone {
				assert.NotContains(t, got, not)
			}
		})
	}
}
