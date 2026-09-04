package guard_test

import (
	"reflect"
	"testing"

	"github.com/kyleking/wavez/internal/guard"
)

// vcrChecks are the checks vcr-tui declares, which is the project whose lane
// spent 9 of its 21 shell calls re-running one of them.
var vcrChecks = []guard.Declared{
	{Name: "format", Command: "uv run ruff format {files}"},
	{Name: "lint", Command: "uv run ruff check {files}"},
	{Name: "types", Command: "uv run ty check src tests"},
	{Name: "test", Command: "uv run pytest -q"},
}

func TestDeclaredCheckRecognizesAProjectsOwnCheck(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		command string
		want    string
		paths   []string
		ok      bool
	}{
		{
			name:    "the lane's own re-run, flags and all",
			command: "uv run ruff check src/vcr_tui/__main__.py tests/conftest.py --select ALL -o concise 2>&1",
			want:    "lint",
			paths:   []string{"src/vcr_tui/__main__.py", "tests/conftest.py"},
			ok:      true,
		},
		{
			name:    "a sweep names no path",
			command: "uv run ruff check . --select ALL",
			want:    "lint",
			ok:      true,
		},
		{
			name:    "the formatter is a different check, not the linter",
			command: "uv run ruff format src/vcr_tui/__main__.py",
			want:    "format",
			paths:   []string{"src/vcr_tui/__main__.py"},
			ok:      true,
		},
		{
			name:    "a check reached through a pipeline stage still counts",
			command: "echo hi; uv run ruff check --no-cache | tail -40",
			want:    "lint",
			ok:      true,
		},
		{
			name:    "a command sharing a prefix but not the tool",
			command: "uv run pytest tests/test_preview",
			want:    "test",
			paths:   []string{"tests/test_preview"},
			ok:      true,
		},
		{name: "an unrelated command", command: "grep -rn TODO src"},
		{name: "the tool without its runner", command: "ruff check src"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, paths, ok := guard.DeclaredCheck(tc.command, vcrChecks)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("DeclaredCheck(%q) = %q, %v, want %q, %v", tc.command, got, ok, tc.want, tc.ok)
			}

			if !reflect.DeepEqual(paths, tc.paths) {
				t.Fatalf("DeclaredCheck(%q) paths = %v, want %v", tc.command, paths, tc.paths)
			}
		})
	}
}

// A project declaring nothing is every Go project, which must keep reaching
// the built-in list rather than matching everything.
func TestDeclaredCheckMatchesNothingWithoutDeclarations(t *testing.T) {
	t.Parallel()

	if _, _, ok := guard.DeclaredCheck("uv run ruff check .", nil); ok {
		t.Fatal("DeclaredCheck matched with no declarations")
	}
}
