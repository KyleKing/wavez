package transcript_test

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/transcript"
)

func readTimeline(t *testing.T, opts ...transcript.TimelineOption) transcript.SessionTimeline {
	t.Helper()

	tl, err := transcript.ReadTimeline(filepath.Join("testdata", "timeline.jsonl"), opts...)
	require.NoError(t, err)

	return tl
}

func kinds(tl *transcript.SessionTimeline) string {
	letters := map[transcript.Kind]string{
		transcript.KindRetrieval: "R", transcript.KindEdit: "E", transcript.KindShell: "S",
		transcript.KindSweep: "W", transcript.KindRefactor: "F", transcript.KindOther: "O",
		transcript.KindProse: "T", transcript.KindPrompt: "P", transcript.KindHook: "G",
		transcript.KindCompact: "C",
	}

	var b strings.Builder
	for i := range tl.Steps {
		b.WriteString(letters[tl.Steps[i].Kind])
	}

	return b.String()
}

func TestTimeline_OrdersTheFixture(t *testing.T) {
	t.Parallel()

	tl := readTimeline(t)

	assert.Equal(t, "PRRREWSSFOTGETGPCTG", kinds(&tl))
	assert.Equal(t, fixtureSession, tl.SessionID)
	assert.Equal(t, "/work/proj", tl.Cwd)
	assert.Equal(t, []string{"claude-test-1"}, tl.Models)
	assert.Len(t, tl.Turns, 13)
	assert.Equal(t, 13, tl.WorkSteps())
	assert.Equal(t, 1, tl.Skipped)
	assert.Equal(t, 67*time.Second, tl.End.Sub(tl.Start))

	for i := range tl.Steps {
		assert.Equal(t, i, tl.Steps[i].Index)
		assert.Empty(t, tl.Steps[i].Detail, "labels only unless detail is asked for")
	}

	read := tl.Steps[1]
	assert.Equal(t, "Read internal/sysinfo/memory.go", read.Label)
	assert.Equal(t, "internal/sysinfo/memory.go", read.Path)
	assert.Equal(t, 1, read.Turn)
	assert.Equal(t, 1, read.Prompt)
	assert.Equal(t, time.Second, read.Duration)

	assert.Equal(t, "Grep UsedFraction", tl.Steps[2].Label, "a turn's text line gives way to its tool call")
	assert.Equal(t, "Bash go build ./... && go test ./...", tl.Steps[5].Label)
	assert.True(t, tl.Steps[5].Failed)
	assert.Equal(t, 7*time.Second, tl.Steps[5].Duration)
	assert.True(t, tl.Steps[6].Denied)
	assert.False(t, tl.Steps[7].Denied)
	assert.Equal(t, "TodoWrite", tl.Steps[9].Label)
	assert.True(t, tl.Steps[11].Blocked)
	assert.Equal(t, "Stop hooks: block: FAIL test", tl.Steps[11].Label)
	assert.Equal(t, `notification: Agent "lint lane" finished`, tl.Steps[15].Label)
	assert.Equal(t, 0, tl.Steps[15].Turn)
}

func TestTimeline_DerivesFromTheFixture(t *testing.T) {
	t.Parallel()

	tl := readTimeline(t)

	assert.Equal(t, []transcript.FileCount{{Path: "internal/sysinfo/memory.go", Count: 2}}, tl.RepeatReads)
	assert.Equal(t, []transcript.Sweep{{Command: "go build", Step: 5}, {Command: "go test", Step: 5}}, tl.Sweeps)
	assert.Equal(t, []transcript.Denial{{
		Suggested: []string{"wavez rename internal/sysinfo/memory.go:Read ReadLog"}, Step: 6, Retried: true,
	}}, tl.Denials)
	assert.Equal(t, []transcript.TestChange{
		{Path: "internal/sysinfo/memory_test.go", Name: `t.Skip("flaky on this machine")`, What: "skipped", Step: 4},
		{Path: "internal/sysinfo/memory_test.go", Name: "TestReadMemory", What: "removed", Step: 4},
	}, tl.TestChanges)
	assert.Equal(t, []string{"internal/sysinfo/memory_test.go", "internal/sysinfo/memory.go"}, tl.Edited)
	assert.Equal(t, 2, tl.Edits)
	assert.Equal(t, 2, tl.TurnsAfterLastEdit)
	assert.Equal(t, 3, tl.RetrievalBeforeEdit)
	assert.Equal(t, transcript.Run{First: 1, Length: 3}, tl.LongestRetrieval)
	assert.Equal(t, 1, tl.Compactions)
	assert.Equal(t, 1, tl.RouteHints)
	assert.Equal(t, 160102, tl.PeakContext)
	assert.Equal(t, transcript.Usage{Input: 26, CacheRead: 190200, CacheCreation: 1600, Output: 1235}, tl.Usage)
	assert.Equal(t, map[string]int{"Stop": 45, "UserPromptSubmit": 109}, tl.HookBytes)
	assert.Equal(t, transcript.Cost{
		USD: 0.4321, Model: 41 * time.Second, Tools: 9 * time.Second, Wall: time.Minute, Recorded: true,
	}, tl.Cost)

	assert.Equal(t, []transcript.Segment{
		{
			Label:  "In internal/sysinfo/memory.go add a method UsedFraction on Memory and c…",
			Prompt: 1, Steps: 12, Turns: 12,
		},
		{Label: `notification: Agent "lint lane" finished`, Prompt: 2, First: 15, Steps: 1, Turns: 1},
	}, tl.Segments)

	edit := tl.Turns[3]
	assert.Equal(t, transcript.KindEdit, edit.Kind)
	assert.Equal(t, transcript.Usage{Input: 2, CacheRead: 1800, CacheCreation: 200, Output: 400}, edit.Usage)
	assert.Equal(t, transcript.Usage{Input: 2, CacheRead: 1200, CacheCreation: 100, Output: 80}, tl.Turns[1].Usage,
		"a message written as several lines keeps the usage of its last one")
}

func TestTimeline_GateRounds(t *testing.T) {
	t.Parallel()

	tl := readTimeline(t)
	require.Len(t, tl.Gates, 3)

	block, note, pass := tl.Gates[0], tl.Gates[1], tl.Gates[2]

	assert.Equal(t, transcript.GateBlock, block.Outcome)
	assert.Equal(t, []transcript.GateCheck{{Name: "test", Status: "fail", Line: "FAIL test"}}, block.Checks)
	assert.Equal(t, 45, block.AgentBytes)
	assert.Zero(t, block.PersonBytes)
	assert.Zero(t, block.Duration, "a blocked stop records no duration")
	assert.Empty(t, block.Agent)

	assert.Equal(t, transcript.GateNote, note.Outcome)
	assert.Equal(t, 2804*time.Millisecond, note.Duration)
	assert.Zero(t, note.AgentBytes)
	assert.Equal(t, 162, note.PersonBytes)
	assert.Equal(t, []transcript.GateCheck{
		{Name: "lint", Status: "not checked", Line: "wavez: NOT CHECKED lint: Error: can't load config"},
		{Name: "test", Status: "already failing", Line: "test: example.com/proj/internal/daemon TestSubscribe"},
	}, note.Checks)

	assert.Equal(t, transcript.GatePass, pass.Outcome)
	assert.Empty(t, pass.Checks)
	assert.Equal(t, 31*time.Millisecond, pass.Duration)
	assert.Equal(t, 18, pass.Step)
}

func TestTimeline_WithDetailKeepsWholeTexts(t *testing.T) {
	t.Parallel()

	tl := readTimeline(t, transcript.WithDetail())

	assert.Equal(t, "go build ./... && go test ./...", tl.Steps[5].Detail)
	assert.Contains(t, tl.Steps[0].Detail, "cover it with a test.")
	assert.Equal(t, "FAIL test\n--- FAIL: TestUsedFraction (0.00s)\n", tl.Gates[0].Agent)
	assert.Contains(t, tl.Gates[1].Person, "TestSubscribe")
}

// line builds one transcript line for a session in /p.
func line(kind, rest string) string {
	return fmt.Sprintf(`{"type":%q,"cwd":"/p",%s}`, kind, rest)
}

func call(id, name, input string) string {
	return line("assistant", fmt.Sprintf(
		`"message":{"id":"m%s","content":[{"type":"tool_use","id":"t%s","name":%q,"input":%s}]}`, id, id, name, input,
	))
}

func prompt(text string) string {
	return line("user", fmt.Sprintf(`"message":{"content":%q}`, text))
}

func readCall(id, path string) string { return call(id, "Read", fmt.Sprintf(`{"file_path":%q}`, path)) }

func editCall(id, path string) string { return call(id, "Edit", fmt.Sprintf(`{"file_path":%q}`, path)) }

func bashCall(id, command string) string {
	return call(id, "Bash", fmt.Sprintf(`{"command":%q}`, command))
}

func proseTurn(id string) string {
	return line("assistant", fmt.Sprintf(`"message":{"id":"m%s","content":[]}`, id))
}

// derivation is a transcript and what its order should say.
type derivation struct {
	check func(t *testing.T, tl *transcript.SessionTimeline)
	name  string
	kinds string
	lines []string
}

func stepDerivations() []derivation {
	return []derivation{
		{
			name:  "an empty transcript has no steps and nothing derived",
			kinds: "",
			check: func(t *testing.T, tl *transcript.SessionTimeline) {
				t.Helper()
				assert.Empty(t, tl.RepeatReads)
				assert.Zero(t, tl.TurnsAfterLastEdit)
				assert.False(t, tl.Cost.Recorded)
				assert.Zero(t, tl.PeakContext)
			},
		},
		{
			name: "reads are counted per file, most read first, and a file outside the project keeps its path",
			lines: []string{
				readCall("1", "/p/a.go"), readCall("2", "/p/b.go"), readCall("3", "/p/b.go"),
				readCall("4", "/p/a.go"), readCall("5", "/p/b.go"), readCall("6", "/etc/hosts"),
			},
			kinds: "RRRRRR",
			check: func(t *testing.T, tl *transcript.SessionTimeline) {
				t.Helper()
				want := []transcript.FileCount{{Path: "b.go", Count: 3}, {Path: "a.go", Count: 2}}
				assert.Equal(t, want, tl.RepeatReads)
				assert.Equal(t, "/etc/hosts", tl.Steps[5].Path)
				assert.Equal(t, transcript.Run{First: 0, Length: 6}, tl.LongestRetrieval)
				assert.Equal(t, 6, tl.RetrievalBeforeEdit, "with no edit every retrieval step counts")
			},
		},
		{
			name: "prose does not break a retrieval run and a shell command does",
			lines: []string{
				bashCall("1", "ls"), readCall("2", "/p/a.go"), proseTurn("3"), readCall("4", "/p/c.go"),
				bashCall("5", "make build"), readCall("6", "/p/d.go"),
				editCall("7", "/p/a.go"), readCall("8", "/p/e.go"),
			},
			kinds: "RRTRSRER",
			check: func(t *testing.T, tl *transcript.SessionTimeline) {
				t.Helper()
				assert.Equal(t, transcript.Run{First: 0, Length: 3}, tl.LongestRetrieval)
				assert.Equal(t, 4, tl.RetrievalBeforeEdit)
				assert.Equal(t, 1, tl.TurnsAfterLastEdit)
			},
		},
		{
			name: "a targeted test is shell, a whole-tree one a sweep, and a wavez verb a refactor",
			lines: []string{
				bashCall("1", "go test ./internal/x -run TestOne"), bashCall("2", "mise exec -- go vet ./..."),
				bashCall("3", "wavez move a.go:Thing b.go"), bashCall("4", "golangci-lint run"),
			},
			kinds: "SWFW",
			check: func(t *testing.T, tl *transcript.SessionTimeline) {
				t.Helper()
				want := []transcript.Sweep{{Command: "go vet", Step: 1}, {Command: "golangci-lint run", Step: 3}}
				assert.Equal(t, want, tl.Sweeps)
				assert.Zero(t, tl.Edits)
				assert.Zero(t, tl.TurnsAfterLastEdit, "nothing was edited, so nothing came after an edit")
			},
		},
		{
			name: "segments split the steps at each prompt and skip the harness's own messages",
			lines: []string{
				readCall("0", "/p/z.go"), prompt("first thing"), readCall("1", "/p/a.go"), editCall("2", "/p/a.go"),
				prompt("<command-name>/compact</command-name>"), prompt("second thing\nwith more"), proseTurn("3"),
			},
			kinds: "RPREPT",
			check: func(t *testing.T, tl *transcript.SessionTimeline) {
				t.Helper()
				assert.Equal(t, []transcript.Segment{
					{Label: "first thing", Prompt: 1, First: 1, Steps: 2, Turns: 2},
					{Label: "second thing", Prompt: 2, First: 4, Steps: 1, Turns: 1},
				}, tl.Segments)
				assert.Zero(t, tl.Steps[0].Prompt, "work before the first prompt belongs to none")
			},
		},
		{
			name: "one turn with two tool calls is one turn of the stronger kind",
			lines: []string{
				readCall("1", "/p/a.go"),
				line("assistant", `"message":{"id":"m1","content":[`+
					`{"type":"tool_use","id":"t9","name":"Write","input":{"file_path":"/p/n.go"}}]}`),
			},
			kinds: "RE",
			check: func(t *testing.T, tl *transcript.SessionTimeline) {
				t.Helper()
				require.Len(t, tl.Turns, 1)
				assert.Equal(t, transcript.KindEdit, tl.Turns[0].Kind)
				assert.False(t, tl.Turns[0].HasUsage)
				assert.Equal(t, 1, tl.Steps[1].Turn)
			},
		},
	}
}

func hookDerivations() []derivation {
	return []derivation{
		{
			name: "a stop hook that failed through its exit status is a block with the text it printed",
			lines: []string{
				proseTurn("1"),
				line("system", `"subtype":"stop_hook_summary","toolUseID":"s1",`+
					`"hookErrors":["[command hook]: push first\n"],"hookInfos":[{}]`),
				line("attachment", `"attachment":{"type":"hook_non_blocking_error","hookEvent":"Stop",`+
					`"toolUseID":"s2","stderr":"wavez hook: boom"}`),
			},
			kinds: "TGG",
			check: func(t *testing.T, tl *transcript.SessionTimeline) {
				t.Helper()
				require.Len(t, tl.Gates, 2)
				assert.Equal(t, transcript.GateBlock, tl.Gates[0].Outcome)
				assert.Equal(t, 27, tl.Gates[0].AgentBytes)
				assert.Equal(t, transcript.GateError, tl.Gates[1].Outcome)
				assert.Equal(t, "wavez hook: boom", tl.Gates[1].Headline)
				assert.Equal(t, map[string]int{"Stop": 27}, tl.HookBytes)
			},
		},
		{
			name: "a release is told apart from a note by what the person was told",
			lines: []string{
				line("attachment", `"attachment":{"type":"hook_system_message","hookEvent":"Stop","toolUseID":"s1",`+
					`"content":"wavez: the gates failed the same way 3 times in a row, `+
					`so the agent was allowed to stop. Still failing:\nFAIL build\n./x.go:3: nope\n"}`),
				line("system", `"subtype":"stop_hook_summary","toolUseID":"s1"`),
			},
			kinds: "G",
			check: func(t *testing.T, tl *transcript.SessionTimeline) {
				t.Helper()
				require.Len(t, tl.Gates, 1)
				assert.Equal(t, transcript.GateReleased, tl.Gates[0].Outcome)
				want := []transcript.GateCheck{{Name: "build", Status: "fail", Line: "FAIL build"}}
				assert.Equal(t, want, tl.Gates[0].Checks)
			},
		},
	}
}

func TestTimeline_Derivations(t *testing.T) {
	t.Parallel()

	for _, tt := range slices.Concat(stepDerivations(), hookDerivations()) {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tl, err := transcript.Timeline(strings.NewReader(strings.Join(tt.lines, "\n")))
			require.NoError(t, err)
			assert.Equal(t, tt.kinds, kinds(&tl))
			tt.check(t, &tl)
		})
	}
}

func TestReadTimeline_CountsSubagentsWithoutAddingTheirSteps(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	main := filepath.Join(dir, "s.jsonl")
	subs := filepath.Join(dir, "s", "subagents")

	require.NoError(t, os.MkdirAll(subs, 0o750))
	require.NoError(t, os.WriteFile(main, []byte(prompt("go")+"\n"), 0o600))

	sub := line("assistant", `"message":{"id":"m1","usage":{"input_tokens":5,"output_tokens":7},`+
		`"content":[{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"/p/a.go"}}]}`)
	require.NoError(t, os.WriteFile(filepath.Join(subs, "agent-1.jsonl"), []byte(sub+"\n"), 0o600))

	tl, err := transcript.ReadTimeline(main)
	require.NoError(t, err)
	assert.Len(t, tl.Steps, 1)
	assert.Equal(t, 1, tl.Subagents)
	assert.Equal(t, 1, tl.SubagentSteps)
	assert.Equal(t, transcript.Usage{Input: 5, Output: 7}, tl.SubagentUsage)

	_, err = transcript.ReadTimeline(filepath.Join(dir, "missing.jsonl"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestLatest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	write := func(folder, id, cwd string, age time.Duration) string {
		path := filepath.Join(root, folder, id+".jsonl")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))

		body := `{"type":"queue-operation"}` + "\n" + fmt.Sprintf(`{"type":"user","cwd":%q}`, cwd) + "\n"
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

		at := time.Now().Add(-age)
		require.NoError(t, os.Chtimes(path, at, at))

		return path
	}

	write("a", "old", "/work/proj", 3*time.Hour)
	newest := write("b", "new", "/work/proj/sub", time.Hour)
	write("c", "other", "/work/project", time.Minute)
	write("d", "nocwd", "", time.Second)

	got, err := transcript.Latest(root, "/work/proj")
	require.NoError(t, err)
	assert.Equal(t, newest, got, "the newest session that ran in the project or below it")

	_, err = transcript.Latest(root, "/elsewhere")

	var notFound *transcript.NotFoundError
	require.ErrorAs(t, err, &notFound)
}
