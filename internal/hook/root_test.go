package hook_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kyleking/wavez/internal/hook"
)

func TestFindRoot(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	write(t, base, "configured/.wavez.toml", "")
	write(t, base, "configured/.git/HEAD", "")
	write(t, base, "configured/tools/go.mod", "module tools\n")
	write(t, base, "configured/tools/cmd/main.go", "package main\n")
	write(t, base, "plain/.git/HEAD", "")
	write(t, base, "plain/nested/go.mod", "module nested\n")
	write(t, base, "plain/nested/pkg/a.go", "package pkg\n")
	write(t, base, "plain/docs/readme.md", "")
	write(t, base, "module/go.mod", "module m\n")
	write(t, base, "module/internal/x/x.go", "package x\n")
	write(t, base, "jj/.jj/repo/store/git/HEAD", "")
	write(t, base, "jj/nested/go.mod", "module nested\n")
	write(t, base, "jj/nested/pkg/a.go", "package pkg\n")

	tests := []struct {
		name string
		dir  string
		want string
	}{
		{name: "the configured root itself", dir: "configured", want: "configured"},
		{name: "a nested module does not shadow the config", dir: "configured/tools/cmd", want: "configured"},
		{name: "a nested module does not shadow the repository", dir: "plain/nested/pkg", want: "plain"},
		{name: "a repository without modules", dir: "plain/docs", want: "plain"},
		{name: "a module outside any repository", dir: "module/internal/x", want: "module"},
		{name: "a directory that does not exist yet", dir: "module/internal/new/deeper", want: "module"},
		{name: "a jj workspace without .git is a repository", dir: "jj/nested/pkg", want: "jj"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, filepath.Join(base, tt.want), hook.FindRoot(filepath.Join(base, tt.dir)))
		})
	}
}

func TestFindRoot_WalkEndsWhereTheParentStopsChanging(t *testing.T) {
	t.Parallel()

	// filepath.Dir(".") is ".", so a walk that waits for "/" never returns.
	for _, dir := range []string{".", "no/such/relative/dir", "", string(filepath.Separator)} {
		assert.NotPanics(t, func() { hook.FindRoot(dir) }, dir)
	}

	assert.Equal(t, string(filepath.Separator), hook.FindRoot(string(filepath.Separator)))
}
