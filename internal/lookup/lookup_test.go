package lookup_test

import (
	"os"
	"os/exec"
	"path/filepath"
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

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("NOT TESTED: git is not on PATH")
	}

	root := t.TempDir()
	files := map[string]string{
		"pkg/types/action.go":      "package types\n\n// ParseActionID parses.\nfunc ParseActionID() {}\n",
		"pkg/types/action_test.go": "package types\n\nfunc TestParse(t *testing.T) { ParseActionID() }\n",
		"pkg/other/far_test.go":    "package other\n\nfunc TestFar(t *testing.T) { types.ParseActionID(\"a\") }\n",
		"pkg/cache/cache.go":       "package cache\n\nfunc (c *Cache[K, V]) popExpired() {}\n",
		"sdk/util/chunks.py":       "def create_chunks(xs, n):\n    return xs\n",
		"examples/a/worker.py":     "def create_chunks(xs, n):\n    return xs\n",
		"examples/b/worker.py":     "def create_chunks(xs, n):\n    return xs\n",
		"sdk/tests/test_chunks.py": "from sdk.util.chunks import create_chunks\n",
		"web/duration.ts":          "export function durationToString(ms: number): string { return '' }\n",
		"web/duration.test.ts":     "import { durationToString } from './duration'\n",
		"web/labels.ts":            "export const TypeScriptLabel = 'ts'\nconst maxRetries = 3\n",
	}

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
		{name: "a name declared in too many places says nothing", prompt: "Rename create_chunks to chunked"},
		{
			name:   "the file the prompt names settles it",
			prompt: "Rename create_chunks in sdk/util/chunks.py to chunked",
			want: "wavez looked these up, so you need not search for them:\n" +
				"create_chunks is declared at sdk/util/chunks.py:1; tests that name it: sdk/tests/test_chunks.py",
		},
		{name: "a capitalized constant is taken for prose", prompt: "the TypeScriptLabel SDK"},
		{
			name: "a lower-case constant is a name", prompt: "raise maxRetries to five",
			want: "wavez looked these up, so you need not search for them:\nmaxRetries is declared at web/labels.ts:2",
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
