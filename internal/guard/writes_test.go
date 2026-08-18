package guard_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kyleking/wavez/internal/guard"
)

func TestWriteTargets(t *testing.T) {
	t.Parallel()

	env := guard.Env{ProjectRoot: "/repo"}

	tests := []struct {
		name    string
		command string
		want    []string
	}{
		{name: "a read is not a write", command: "go test ./internal/api", want: nil},
		{name: "redirect", command: "echo hi > internal/api/out.txt", want: []string{"/repo/internal/api/out.txt"}},
		{name: "append", command: "echo hi >> notes.md", want: []string{"/repo/notes.md"}},
		{name: "tee", command: "printf x | tee -a internal/vcs/log", want: []string{"/repo/internal/vcs/log"}},
		{
			// The script argument reads as a path from the text alone, which is
			// why the caller filters the candidates against the tree.
			name:    "in-place edit",
			command: "sed -i '' s/a/b/ internal/vcs/jj.go",
			want:    []string{"/repo/internal/vcs/jj.go", "/repo/s/a/b"},
		},
		{
			name:    "formatter behind a flag",
			command: "gofmt -w internal/tui/home.go",
			want:    []string{"/repo/internal/tui/home.go"},
		},
		{name: "formatter without one", command: "black src", want: []string{"/repo/src"}},
		{name: "move", command: "mv a.go internal/api/b.go", want: []string{"/repo/a.go", "/repo/internal/api/b.go"}},
		{name: "remove", command: "rm -rf internal/tmp", want: []string{"/repo/internal/tmp"}},
		{name: "absolute target stays absolute", command: "echo x > /tmp/scratch", want: []string{"/tmp/scratch"}},
		{name: "a target the guard cannot reduce is dropped", command: "echo x > $OUT/file", want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, guard.WriteTargets(tc.command, env))
		})
	}
}
