package bench_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/bench"
)

const rev = "2a35893c95a388d8bf5f0946c6bfc2b0caed1d76"

func TestLoadTasks_ResolvesRepos(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "tasks.toml")
	write(t, path, `
[[task]]
id = "here"
prompt = "Do the thing."
rev = "`+rev+`"
checks = ["README.md:Status: done.", "!answer:sorry", "build:./...", "test:./x/...", "cmd:test -f a.txt"]

[[task]]
id = "sibling"
prompt = "Do it elsewhere."
repo = "../other"
rev = "abc1234"

[[task]]
id = "remote"
prompt = "Do it far away."
repo = "https://example.invalid/r.git"
rev = "abc1234"

[[task]]
id = "absolute"
prompt = "Do it at a fixed place."
repo = "/srv/repo"
rev = "abc1234"
`)

	tasks, err := bench.LoadTasks(path)
	require.NoError(t, err)
	require.Len(t, tasks, 4)

	assert.Equal(t, dir, tasks[0].Repo)
	assert.Equal(t, rev, tasks[0].Rev)
	assert.Len(t, tasks[0].Checks, 5)
	assert.Equal(t, filepath.Join(filepath.Dir(dir), "other"), tasks[1].Repo)
	assert.Equal(t, "https://example.invalid/r.git", tasks[2].Repo)
	assert.Equal(t, "/srv/repo", tasks[3].Repo)

	found, err := bench.FindTask(tasks, "remote")
	require.NoError(t, err)
	assert.Equal(t, "Do it far away.", found.Prompt)

	_, err = bench.FindTask(tasks, "nope")

	var unknown *bench.UnknownTaskError

	require.ErrorAs(t, err, &unknown)
	assert.Equal(t, "nope", unknown.ID)
	assert.Contains(t, err.Error(), "nope")
}

func TestLoadTasks_Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		body     string
		wantTask string
		wantKey  string
	}{
		{"missing id", `[[task]]` + "\n" + `prompt = "p"` + "\n" + `rev = "abc1234"`, "", "id"},
		{"missing prompt", `[[task]]` + "\n" + `id = "a"` + "\n" + `rev = "abc1234"`, "a", "prompt"},
		{"missing rev", `[[task]]` + "\n" + `id = "a"` + "\n" + `prompt = "p"`, "a", "rev"},
		{"branch as rev", `[[task]]` + "\n" + `id = "a"` + "\n" + `prompt = "p"` + "\n" + `rev = "main"`, "a", "rev"},
		{
			"bad check",
			`[[task]]` + "\n" + `id = "a"` + "\n" + `prompt = "p"` + "\n" + `rev = "abc1234"` + "\n" +
				`checks = ["answer:ok", "nocolon"]`,
			"a", "checks[1]",
		},
		{
			"duplicate id",
			`[[task]]` + "\n" + `id = "a"` + "\n" + `prompt = "p"` + "\n" + `rev = "abc1234"` + "\n" +
				`[[task]]` + "\n" + `id = "a"` + "\n" + `prompt = "q"` + "\n" + `rev = "abc1234"`,
			"a", "id",
		},
		{
			"unknown key",
			`[[task]]` + "\n" + `id = "a"` + "\n" + `prompt = "p"` + "\n" + `rev = "abc1234"` + "\n" + `extra = []`,
			"", "task.extra",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "tasks.toml")
			write(t, path, tt.body)

			_, err := bench.LoadTasks(path)

			var invalid *bench.ValidationError

			require.ErrorAs(t, err, &invalid)
			assert.Equal(t, tt.wantTask, invalid.Task)
			assert.Equal(t, tt.wantKey, invalid.Key)
			assert.Contains(t, err.Error(), tt.wantKey)
		})
	}
}

func TestLoadTasks_UnreadableFile(t *testing.T) {
	t.Parallel()

	_, err := bench.LoadTasks(filepath.Join(t.TempDir(), "absent.toml"))
	require.Error(t, err)

	path := filepath.Join(t.TempDir(), "tasks.toml")
	write(t, path, "[[task]\n")

	_, err = bench.LoadTasks(path)
	require.Error(t, err)
}

func TestTask_Hash(t *testing.T) {
	t.Parallel()

	base := bench.Task{ID: "a", Prompt: "p", Rev: rev, Checks: mustChecks(t, "answer:ok")}

	tests := []struct {
		change func(task *bench.Task)
		name   string
		same   bool
	}{
		{name: "unchanged", change: func(*bench.Task) {}, same: true},
		{name: "where the repo resolved to", change: func(task *bench.Task) { task.Repo = "/elsewhere" }, same: true},
		{name: "prompt", change: func(task *bench.Task) { task.Prompt = "q" }},
		{name: "rev", change: func(task *bench.Task) { task.Rev = "abc1234" }},
		{name: "checks", change: func(task *bench.Task) { task.Checks = mustChecks(t, "!answer:ok") }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			changed := base
			tt.change(&changed)

			assert.Len(t, changed.Hash(), 12)
			assert.Equal(t, tt.same, base.Hash() == changed.Hash())
		})
	}
}
