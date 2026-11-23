package hook_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/hook"
)

func TestHandle_Stop_TellsThePersonAboutWeakenedTests(t *testing.T) {
	t.Parallel()

	const (
		head     = "package a\n\nimport \"testing\"\n\n"
		rest     = "func TestGone(t *testing.T) {}\n\nfunc TestMoved(t *testing.T) {}\n"
		original = head + "func TestKept(t *testing.T) {}\n\n" + rest
	)

	tests := []struct {
		name string
		// files is what the session leaves in each test file.
		files map[string]string
		want  string
	}{
		{
			name:  "a skip the session added",
			files: map[string]string{"a_test.go": head + "func TestKept(t *testing.T) { t.Skip() }\n\n" + rest},
			want:  "wavez: 1 file changed · checks passed · review: skip added in a_test.go",
		},
		{
			name: "a test removed, and one that only moved to another file",
			files: map[string]string{
				"a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestKept(t *testing.T) {}\n",
				"b_test.go": "package a\n\nimport \"testing\"\n\nfunc TestMoved(t *testing.T) {}\n",
			},
			want: "wavez: 2 files changed · checks passed · review: TestGone removed from a_test.go",
		},
		{
			name:  "an edit inside a test is nothing to review",
			files: map[string]string{"a_test.go": head + "func TestKept(t *testing.T) { t.Log(1) }\n\n" + rest},
			want:  "wavez: 1 file changed · checks passed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := repo(t)
			write(t, root, "a_test.go", original)
			write(t, root, "b_test.go", "package a\n")
			git(t, root, "add", ".")
			git(t, root, "commit", "-qm", "tests")

			h := hook.New(&fakeRunner{verdicts: []hook.Verdict{green}})
			start(t, h, root)

			for name, content := range tt.files {
				write(t, root, name, content)
			}

			res, err := fire(t, h, stop(root, false))
			require.NoError(t, err)
			assert.Equal(t, tt.want, res.SystemMessage)
			assert.Empty(t, res.Decision, "it is the person's to judge, so the agent is not held")
			assert.Empty(t, res.Specific.AdditionalContext)
		})
	}
}
