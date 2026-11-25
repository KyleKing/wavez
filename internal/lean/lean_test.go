package lean_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kyleking/wavez/internal/lean"
)

func TestArgs(t *testing.T) {
	t.Parallel()

	both := []string{"--system-prompt", lean.Prompt, "--tools", lean.Tools}

	tests := []struct {
		name string
		rest []string
		want []string
	}{
		{name: "nothing passed", want: both},
		{name: "the rest follows", rest: []string{"-p", "fix it"}, want: append(both[:4:4], "-p", "fix it")},
		{
			name: "tools named by the caller are the caller's",
			rest: []string{"--tools", "Bash,Read,Edit,Write"},
			want: []string{"--system-prompt", lean.Prompt, "--tools", "Bash,Read,Edit,Write"},
		},
		{
			name: "a prompt given with an equals sign counts",
			rest: []string{"--system-prompt=mine"},
			want: []string{"--tools", lean.Tools, "--system-prompt=mine"},
		},
		{
			name: "a prompt file counts",
			rest: []string{"--system-prompt-file", "p.md"},
			want: []string{"--tools", lean.Tools, "--system-prompt-file", "p.md"},
		},
		{
			name: "a flag that only starts the same way does not",
			rest: []string{"--toolsmith"},
			want: append(both[:4:4], "--toolsmith"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, lean.Args(tt.rest...))
		})
	}
}
