package guard_test

import (
	"testing"

	"github.com/kyleking/wavez/internal/guard"
)

func TestEcosystemCommands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		names   []string
		command string
		want    guard.Verdict
	}{
		{name: "no bundle", command: "uv run pytest -q", want: guard.NeedsApproval},
		{name: "python bundle", names: []string{"python"}, command: "uv run pytest -q", want: guard.Allow},
		{
			// permission.Store keys an answer on the whole command line, so
			// the same program with a different tail is a second prompt
			// unless the program itself is allowed.
			name: "python bundle covers a variant", names: []string{"python"},
			command: "uv run ruff check db_slice | head -n 5", want: guard.Allow,
		},
		{name: "one bundle is not another", names: []string{"rust"}, command: "pytest -q", want: guard.NeedsApproval},
		{
			name:  "a name outside every bundle contributes nothing",
			names: []string{"klingon"}, command: "pytest -q", want: guard.NeedsApproval,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := guard.Env{ProjectRoot: "/repo", AllowedCommands: guard.EcosystemCommands(tt.names)}
			if got := guard.Classify(tt.command, env).Verdict; got != tt.want {
				t.Errorf("Classify(%q) = %v, want %v", tt.command, got, tt.want)
			}
		})
	}
}
