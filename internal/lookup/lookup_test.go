package lookup_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/lookup"
)

func TestNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		prompt string
		want   []string
	}{
		{prompt: "ParseActionID in pkg/client/types accepts an empty part", want: []string{"ParseActionID"}},
		{prompt: "timedelta_to_expr truncates, and timedelta_to_expr is wrong", want: []string{"timedelta_to_expr"}},
		{prompt: "Rename num to formatNumber everywhere", want: []string{"formatNumber"}},
		{prompt: "The password rule measures length in bytes. Fix the Retry header."},
		{prompt: "a b c parseA getID", want: []string{"parseA", "getID"}},
		{
			prompt: "oneTwo threeFour fiveSix sevenEight nineTen",
			want:   []string{"oneTwo", "threeFour", "fiveSix", "sevenEight"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.prompt, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, lookup.Names(tt.prompt))
		})
	}
}

// repo is a committed tree in three languages.
func repo(t *testing.T) string {
	t.Helper()

	return committed(t, map[string]string{
		"pkg/types/action.go":      "package types\n\n// ParseActionID parses.\nfunc ParseActionID() {}\n",
		"pkg/types/action_test.go": "package types\n\nfunc TestParse(t *testing.T) { ParseActionID() }\n",
		"pkg/other/far_test.go":    "package other\n\nfunc TestFar(t *testing.T) { types.ParseActionID(\"a\") }\n",
		"pkg/cache/cache.go":       "package cache\n\nfunc (c *Cache[K, V]) popExpired() {}\n",
		"sdk/util/chunks.py":       "def create_chunks(xs, n):\n    return xs\n",
		"examples/a/worker.py":     "def create_chunks(xs, n):\n    return xs\n",
		"examples/b/worker.py":     "def create_chunks(xs, n):\n    return xs\n",
		"top.py":                   "def create_chunks(xs, n):\n    return xs\n",
		"sdk/tests/test_chunks.py": "from sdk.util.chunks import create_chunks\n",
		"web/duration.ts":          "export function durationToString(ms: number): string { return '' }\n",
		"web/duration.test.ts":     "import { durationToString } from './duration'\n",
		"web/labels.ts":            "export const TypeScriptLabel = 'ts'\nconst maxRetries = 3\n",
		"pkg/valid/valid.go": "package valid\n\nfunc passwordValidation(pw string) bool { return pw != \"\" }\n\n" +
			"func headerName() string { return \"x\" }\n\nfunc parseRetryAfter(h string) error {\n" +
			"\tif h == \"\" {\n\t\treturn errors.New(\"unsupported retry header value\")\n\t}\n\n\treturn nil\n}\n",
		"pkg/cache/ttl.go": "package cache\n\ntype TTLCache struct{}\n",
	})
}

// committed is a new repository with files committed.
func committed(t *testing.T, files map[string]string) string {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("NOT TESTED: git is not on PATH")
	}

	root := t.TempDir()

	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	}

	for _, args := range [][]string{
		{"init", "-q"},
		{"add", "-A"},
		{"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-qm", "base"},
	} {
		cmd := exec.CommandContext(t.Context(), "git", args...) //nolint:gosec // fixed program, test arguments
		cmd.Dir = root

		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
	}

	return root
}

func TestFind(t *testing.T) {
	t.Parallel()

	root := repo(t)

	tests := []struct {
		name   string
		prompt string
		want   string
	}{
		{
			name: "a Go function with a test beside it", prompt: "ParseActionID accepts an empty part",
			want: "wavez looked these up, so you need not search for them:\n" +
				"ParseActionID is declared at pkg/types/action.go:4; tests that name it: pkg/types/action_test.go\n" +
				"To check: go test ./pkg/types/",
		},
		{
			name: "a method with no test", prompt: "popExpired never runs",
			want: "wavez looked these up, so you need not search for them:\n" +
				"popExpired is declared at pkg/cache/cache.go:3\nTo check: go test ./pkg/cache/",
		},
		{
			name: "a TypeScript function", prompt: "durationToString drops the fraction",
			want: "wavez looked these up, so you need not search for them:\n" +
				"durationToString is declared at web/duration.ts:1; tests that name it: web/duration.test.ts",
		},
		{name: "a name declared in too many places says nothing", prompt: "Rename create_chunks to chunked."},
		{
			name:   "the file the prompt names settles it",
			prompt: "Rename create_chunks in sdk/util/chunks.py to chunked",
			want: "wavez looked these up, so you need not search for them:\n" +
				"create_chunks is declared at sdk/util/chunks.py:1; tests that name it: sdk/tests/test_chunks.py",
		},
		{name: "a capitalized constant is taken for prose", prompt: "the TypeScriptLabel SDK"},
		{
			name: "a lower-case constant is a name", prompt: "raise maxRetries to five",
			want: "wavez looked these up, so you need not search for them:\n" +
				"maxRetries is declared at web/labels.ts:2; " +
				"no test names it, the tests beside it: web/duration.test.ts",
		},
		{name: "a name declared nowhere", prompt: "add NarrowPtr beside it"},
		{name: "no identifier", prompt: "the password rule counts bytes"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, lookup.Hint(lookup.Find(t.Context(), root, tt.prompt)))
		})
	}

	assert.Empty(t, lookup.Find(t.Context(), t.TempDir(), "ParseActionID"), "outside a repository nothing is found")
}

func TestBrief(t *testing.T) {
	t.Parallel()

	root := repo(t)

	got := lookup.Brief(root, lookup.Find(t.Context(), root, "ParseActionID accepts an empty part"))
	for _, want := range []string{
		"ParseActionID is declared at pkg/types/action.go:4",
		"--- pkg/types/action.go lines 3-4\n3\t// ParseActionID parses.\n4\tfunc ParseActionID() {}\n",
		"--- pkg/types/action_test.go lines 3-3\n3\tfunc TestParse(t *testing.T) { ParseActionID() }\n",
		"You have the source, so skip reading",
		"wavez apply <<'WAVEZ'",
	} {
		assert.Contains(t, got, want)
	}

	assert.NotContains(t, got, "far_test.go", "a test elsewhere is left out when one sits beside the declaration")

	python := lookup.Brief(root, lookup.Find(t.Context(), root, "create_chunks in sdk/util/chunks.py drops the tail"))
	assert.Contains(t, python, "--- sdk/util/chunks.py lines 1-2\n1\tdef create_chunks(xs, n):\n2\t    return xs\n")
	assert.Contains(t, python, "--- sdk/tests/test_chunks.py lines 1-1\n")

	assert.Empty(t, lookup.Brief(root, nil))
}

func TestExcerpts_StopAtTheBudget(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	body := strings.Repeat("\tprintln(\"a line of the body that is long enough to add up\")\n", 400)
	long := "package big\n\nfunc BigOne() {\n" + body + "}\n"

	require.NoError(t, os.WriteFile(filepath.Join(root, "big.go"), []byte(long), 0o600))

	got := lookup.Excerpts(root, []lookup.Site{{Name: "BigOne", File: "big.go", Line: 3}})
	assert.Equal(t, "(more left out: read big.go for the rest)\n", got)
}

func TestFind_FromADirectoryAndAPhrase(t *testing.T) {
	t.Parallel()

	root := repo(t)

	tests := []struct {
		name   string
		prompt string
		want   []string
	}{
		{
			name: "a word of the prompt in a name of the directory it names", want: []string{"passwordValidation"},
			prompt: "The password rule in pkg/valid measures length in bytes.",
		},
		{
			name: "the longer word wins over a shorter one", want: []string{"parseRetryAfter"},
			prompt: "The Retry-After header is refused in pkg/valid when it is padded.",
		},
		{
			name: "the declaration around a quoted message", want: []string{"parseRetryAfter"},
			prompt: "It fails with 'unsupported retry header value' on a padded value.",
		},
		{
			name: "a name that opens with capitals", want: []string{"TTLCache"},
			prompt: "TTLCache has no way to ask how many entries it holds.",
		},
		{name: "a directory that is not there", prompt: "The password rule in pkg/nowhere is wrong."},
		{name: "a word no name holds", prompt: "The widget rule in pkg/valid is wrong."},
		{name: "a short quoted value is not searched for", prompt: "It turns '1500' into '1s'."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got []string
			for _, site := range lookup.Find(t.Context(), root, tt.prompt) {
				got = append(got, site.Name)
			}

			assert.Equal(t, tt.want, got)
		})
	}
}

func TestBrief_ShowsWhatAFunctionReadsAndSaysWhenTestsAreMissing(t *testing.T) {
	t.Parallel()

	root := committed(t, map[string]string{
		"scrub/rules.go": "package scrub\n\nconst masked = \"[x]\"\n\nvar (\n\tother = 1\n" +
			"\t// tokenRules are tried in order.\n\ttokenRules = []string{\"a\"}\n)\n\ntype unused struct{}\n",
		"scrub/scrub.go": "package scrub\n\nfunc scrubTokens(s string) string {\n" +
			"\tfor range tokenRules {\n\t\ts = masked + s.other\n\t}\n\n\treturn s\n}\n",
		"scrub/table_test.go": "package scrub\n\nfunc TestTable(t *testing.T) { scrub(\"a\") }\n",
	})

	got := lookup.Brief(root, lookup.Find(t.Context(), root, "scrubTokens lets a GitLab token through"))

	assert.Contains(t, got, "no test names it, the tests beside it: scrub/table_test.go")
	assert.Contains(t, got, "--- scrub/rules.go lines 3-3\n3\tconst masked")
	assert.Contains(t, got, "--- scrub/rules.go lines 7-8\n7\t\t// tokenRules are tried in order.")
	assert.NotContains(t, got, "other = 1", "a field the function selects is not a variable it reads")
	assert.NotContains(t, got, "unused")
	assert.Contains(t, got, "Read what else the change needs in one message")
	assert.NotContains(t, got, "skip reading")
}
