package bench_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/bench"
)

func TestParseCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		text string
		want bench.Check
	}{
		{"README.md:Status: done.", bench.Check{Kind: bench.CheckFile, Path: "README.md", Want: "Status: done."}},
		{"!a/b.go:func Old(", bench.Check{Kind: bench.CheckFile, Path: "a/b.go", Want: "func Old(", Negate: true}},
		{"answer:rules.go", bench.Check{Kind: bench.CheckAnswer, Want: "rules.go"}},
		{"!answer:sorry", bench.Check{Kind: bench.CheckAnswer, Want: "sorry", Negate: true}},
		{"build:./...", bench.Check{Kind: bench.CheckBuild, Want: "./..."}},
		{"test:./internal/x/...", bench.Check{Kind: bench.CheckTest, Want: "./internal/x/..."}},
		{"cmd:uv run pytest tests/test_a.py", bench.Check{Kind: bench.CheckCmd, Want: "uv run pytest tests/test_a.py"}},
		{"!cmd:grep -q TODO: a.py", bench.Check{Kind: bench.CheckCmd, Want: "grep -q TODO: a.py", Negate: true}},
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			t.Parallel()

			got, err := bench.ParseCheck(tt.text)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.text, got.String())
		})
	}
}

func TestParseCheck_Rejects(t *testing.T) {
	t.Parallel()

	for _, text := range []string{"", "!", "  ", "nocolon", "answer:", ":substring"} {
		t.Run(text, func(t *testing.T) {
			t.Parallel()

			_, err := bench.ParseCheck(text)
			require.Error(t, err)
		})
	}
}

// goModule writes a module whose one package builds, and whose test passes
// only when ok is true.
func goModule(t *testing.T, ok bool) string {
	t.Helper()

	dir := t.TempDir()
	write(t, filepath.Join(dir, "go.mod"), "module example.invalid/m\n\ngo 1.24\n")
	write(t, filepath.Join(dir, "m.go"), "package m\n\n// Value is fixed.\nfunc Value() bool { return "+
		map[bool]string{true: "true", false: "false"}[ok]+" }\n")
	write(t, filepath.Join(dir, "m_test.go"), "package m\n\nimport \"testing\"\n\n"+
		"func TestValue(t *testing.T) {\n\tif !Value() {\n\t\tt.Fatal(\"value is false\")\n\t}\n}\n")

	return dir
}

func TestVerify(t *testing.T) {
	t.Parallel()

	passing, failing := goModule(t, true), goModule(t, false)
	broken := t.TempDir()
	write(t, filepath.Join(broken, "go.mod"), "module example.invalid/b\n\ngo 1.24\n")
	write(t, filepath.Join(broken, "b.go"), "package b\n\nfunc Broken() { undefined() }\n")

	tests := []struct {
		name       string
		dir        string
		check      string
		answer     string
		wantDetail string
		wantPass   bool
	}{
		{name: "file holds the substring", dir: passing, check: "m.go:func Value()", wantPass: true},
		{name: "file lacks the substring", dir: passing, check: "m.go:func Other()", wantDetail: "not satisfied"},
		{name: "file is absent", dir: passing, check: "absent.go:x", wantDetail: "no such file"},
		{name: "inverted file check holds", dir: passing, check: "!m.go:func Other()", wantPass: true},
		{name: "inverted file check on an absent file", dir: passing, check: "!absent.go:x", wantPass: true},
		{name: "inverted file check fails", dir: passing, check: "!m.go:func Value()", wantDetail: "must not"},
		{name: "answer holds", dir: passing, check: "answer:rules.go", answer: "It is rules.go.", wantPass: true},
		{name: "answer lacks", dir: passing, check: "answer:rules.go", answer: "No idea.", wantDetail: "not satisfied"},
		{name: "inverted answer", dir: passing, check: "!answer:sorry", answer: "Done.", wantPass: true},
		{name: "build passes", dir: passing, check: "build:./...", wantPass: true},
		{name: "build fails", dir: broken, check: "build:./...", wantDetail: "undefined"},
		{name: "inverted build", dir: broken, check: "!build:./...", wantPass: true},
		{name: "test passes", dir: passing, check: "test:./...", wantPass: true},
		{name: "test fails", dir: failing, check: "test:./...", wantDetail: "FAIL"},
		{name: "cmd passes", dir: passing, check: "cmd:test -f m.go && grep -q Value m.go", wantPass: true},
		{name: "cmd fails with output", dir: passing, check: "cmd:echo nope; exit 3", wantDetail: "nope"},
		{name: "cmd fails silently", dir: passing, check: "cmd:exit 3", wantDetail: "exit status 3"},
		{name: "inverted cmd", dir: passing, check: "!cmd:test -f absent.txt", wantPass: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := bench.Verify(t.Context(), mustChecks(t, tt.check), tt.dir, tt.answer, time.Minute)
			require.Len(t, got, 1)

			assert.Equal(t, tt.check, got[0].Check)
			assert.Equal(t, tt.wantPass, got[0].Pass, got[0].Detail)
			assert.Contains(t, got[0].Detail, tt.wantDetail)
			assert.Equal(t, tt.wantPass, bench.Passed(got))

			if tt.wantPass {
				assert.Empty(t, got[0].Detail)
			}
		})
	}
}

func TestVerify_UnknownKindFails(t *testing.T) {
	t.Parallel()

	got := bench.Verify(t.Context(), []bench.Check{{Kind: "smell", Want: "x"}}, t.TempDir(), "", time.Minute)
	require.Len(t, got, 1)
	assert.False(t, got[0].Pass)
	assert.Contains(t, got[0].Detail, "smell")
}

func TestVerify_DetailIsOneBoundedLine(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("é", 400)

	check := "cmd:printf '\\n# heading\\n" + long + "\\nsecond'; exit 1"

	got := bench.Verify(t.Context(), mustChecks(t, check), t.TempDir(), "", time.Minute)
	require.Len(t, got, 1)

	assert.NotContains(t, got[0].Detail, "\n")
	assert.NotContains(t, got[0].Detail, "heading")
	assert.LessOrEqual(t, len(got[0].Detail), 210)
	assert.True(t, strings.HasSuffix(got[0].Detail, "…"))
}

func TestPassed_NoChecksIsNotAPass(t *testing.T) {
	t.Parallel()

	assert.False(t, bench.Passed(nil))
	assert.True(t, bench.Passed([]bench.CheckResult{{Pass: true}}))
	assert.False(t, bench.Passed([]bench.CheckResult{{Pass: true}, {Pass: false}}))
}
