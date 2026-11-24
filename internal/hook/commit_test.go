package hook_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/hook"
	"github.com/kyleking/wavez/internal/session"
)

func TestRecords(t *testing.T) {
	t.Parallel()

	tests := []struct {
		command string
		want    bool
	}{
		{command: `git commit -m "fix"`, want: true},
		{command: `git add -A && git commit -qm fix`, want: true},
		{command: "git commit -m \"$(cat <<'EOF'\nfix: a thing\n\ngit push is described here\nEOF\n)\"", want: true},
		{command: `git -C sub -c user.name=t commit --amend --no-edit`, want: true},
		{command: `HK=0 git push -u origin main`, want: true},
		{command: `jj commit -m fix`, want: true},
		{command: `jj -R ../ws git push --bookmark main`, want: true},
		{command: `bash -c 'git add -A && git commit -m fix'`, want: true},
		{command: `env GIT_EDITOR=true git commit --amend`, want: true},
		{command: `timeout 60 git push`, want: true},
		{command: `exec git push origin HEAD`, want: true},
		{command: `jj --config ui.color=never git push`, want: true},
		{command: `gh pr create --fill`, want: true},
		{command: `git push --dry-run`},
		{command: `git commit --help`},
		{command: `gh pr view 3`},
		{command: `bash -c 'git status'`},
		{command: `git status && git log -1`},
		{command: `git commit-tree HEAD^{tree}`},
		{command: `jj git fetch`},
		{command: `jj describe -m fix`},
		{command: `echo git commit`},
		{command: `grep -rn "git push" docs/`},
		{command: `git commit -m "unterminated`},
		{command: ``},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, hook.CommitsOrPushes(tt.command))
		})
	}
}

func TestHandle_PreToolUse_HoldsACommitOfUncheckedChangesOnce(t *testing.T) {
	t.Parallel()

	root := repo(t)
	handler := hook.New(&fakeRunner{}, hook.WithCommitChecks(), hook.WithoutStopChecks())

	_, err := fire(t, handler, edit(root, "Edit", "a.go"))
	require.NoError(t, err)
	write(t, root, "a.go", "package a\n\nfunc A() {}\n")

	commit := shell(root, sessionID, `git commit -qam "add A"`)

	res, err := fire(t, handler, commit)
	require.NoError(t, err)
	require.Equal(t, "deny", res.Specific.PermissionDecision, res.raw)
	assert.Contains(t, res.Specific.PermissionReason, "`wavez check`")

	res, err = fire(t, handler, commit)
	require.NoError(t, err)
	assert.Empty(t, res.raw, "the same commit sent again runs")

	// A further change is a change set nobody has checked or been asked about.
	write(t, root, "a.go", "package a\n\nfunc A() {}\n\nfunc B() {}\n")

	res, err = fire(t, handler, commit)
	require.NoError(t, err)
	assert.Equal(t, "deny", res.Specific.PermissionDecision, res.raw)
}

func TestHandle_PreToolUse_LetsACommitThrough(t *testing.T) {
	t.Parallel()

	tests := []struct {
		// settle is what the checks last said about the change set.
		settle  func(state *session.State, changeSet string)
		name    string
		command string
		opts    []hook.Option
		clean   bool
	}{
		{
			name: "the checks passed on these changes", opts: []hook.Option{hook.WithCommitChecks()},
			command: `git commit -qam x`, settle: func(s *session.State, id string) { s.Pass(id) },
		},
		{
			name: "the checks could not run on these changes", opts: []hook.Option{hook.WithCommitChecks()},
			command: `jj git push`, settle: func(s *session.State, id string) { s.Abstain(id) },
		},
		{name: "nothing changed", opts: []hook.Option{hook.WithCommitChecks()}, command: `git push`, clean: true},
		{name: "a command that records nothing", opts: []hook.Option{hook.WithCommitChecks()}, command: `git status`},
		{name: "commit checks are off", command: `git commit -qam x`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := repo(t)
			handler := hook.New(&fakeRunner{}, tt.opts...)

			// A session that began and wrote nothing has no change to check.
			_, err := fire(t, handler, map[string]any{
				"session_id": sessionID, "cwd": root, "hook_event_name": "SessionStart", "source": "startup",
			})
			require.NoError(t, err)

			if !tt.clean {
				write(t, root, "a.go", "package a\n\nfunc A() {}\n")
			}

			if tt.settle != nil {
				id, err := session.Fingerprint(root, []string{"a.go"})
				require.NoError(t, err)

				_, err = session.NewStore(root).Update(t.Context(), sessionID, func(s *session.State) error {
					tt.settle(s, id)

					return nil
				})
				require.NoError(t, err)
			}

			res, err := fire(t, handler, shell(root, sessionID, tt.command))
			require.NoError(t, err)
			assert.Empty(t, res.raw)
		})
	}
}
