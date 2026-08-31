package guard_test

import (
	"path/filepath"
	"testing"

	"github.com/kyleking/wavez/internal/guard"
)

func TestProtectedWrite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rel  string
		want string
	}{
		{name: "the approvals themselves", rel: ".wavez/approvals.jsonl", want: ".wavez/approvals.jsonl"},
		{name: "the project config carrying shellAllow", rel: ".wavez.pkl", want: ".wavez.pkl"},
		{name: "a commit hook", rel: ".git/hooks/pre-commit", want: ".git/"},
		{name: "the git config a hook path lives in", rel: ".git/config", want: ".git/"},
		{name: "a nested checkout's hooks", rel: "vendor/dep/.git/hooks/pre-commit", want: ".git/"},
		{name: "the jj repo", rel: ".jj/repo/store", want: ".jj/"},
		{name: "a workflow", rel: ".github/workflows/ci.yml", want: ".github/workflows/"},
		{name: "the hook config", rel: "hk.pkl", want: "hk.pkl"},
		{name: "a mise task file", rel: ".config/mise/conf.d/project.toml", want: ".config/mise/conf.d/"},
		{name: "the mise config itself", rel: ".config/mise.toml", want: ".config/mise.toml"},
		{name: "a fixture that shares a protected name", rel: "internal/x/testdata/hk.pkl", want: ""},
		{name: "an issue template beside the workflows", rel: ".github/ISSUE_TEMPLATE.md", want: ""},
		{name: "the run's own log", rel: ".wavez/threads/p-1.jsonl", want: ""},
		{name: "ordinary source", rel: "internal/tools/write.go", want: ""},
	}

	root := t.TempDir()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			abs := filepath.Join(root, filepath.FromSlash(tt.rel))
			if got := guard.ProtectedWrite(root, abs); got != tt.want {
				t.Errorf("ProtectedWrite(%s) = %q, want %q", tt.rel, got, tt.want)
			}
		})
	}
}

// A path outside the root is not this rule's to judge: resolvePath refuses
// it first, and answering here would name a protected location for a file
// no tool can reach anyway.
func TestProtectedWrite_SaysNothingAboutAPathOutsideTheRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if got := guard.ProtectedWrite(root, filepath.Join(root, "..", "other", "hk.pkl")); got != "" {
		t.Errorf("ProtectedWrite outside the root = %q, want %q", got, "")
	}
}
