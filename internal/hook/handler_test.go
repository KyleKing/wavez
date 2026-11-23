package hook_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/hook"
	"github.com/kyleking/wavez/internal/session"
)

const sessionID = "5e55-10n"

var errRunner = errors.New("gate config is broken\nsecond line")

type call struct {
	stage   gate.Stage
	changed []string
}

// fakeRunner answers each run with the next scripted verdict, repeating the
// last one, and remembers what it was asked.
type fakeRunner struct {
	verdicts []hook.Verdict
	err      error
	// onRun stands in for a check's fixer rewriting files during the run.
	onRun func()
	calls []call
}

func (f *fakeRunner) Run(_ context.Context, _ string, changed []string, stage gate.Stage) (hook.Verdict, error) {
	f.calls = append(f.calls, call{stage: stage, changed: changed})

	if f.onRun != nil {
		f.onRun()
	}

	if f.err != nil {
		return hook.Verdict{}, f.err
	}

	if len(f.verdicts) == 0 {
		return hook.Verdict{}, nil
	}

	verdict := f.verdicts[0]
	if len(f.verdicts) > 1 {
		f.verdicts = f.verdicts[1:]
	}

	return verdict, nil
}

func (f *fakeRunner) stages() []gate.Stage {
	stages := make([]gate.Stage, 0, len(f.calls))
	for _, c := range f.calls {
		stages = append(stages, c.stage)
	}

	return stages
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()

	full := append(strings.Fields("-c user.name=t -c user.email=t@example.com -c commit.gpgsign=false"), args...)
	cmd := exec.CommandContext(t.Context(), "git", full...) //nolint:gosec // fixed program, test arguments
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

// repo returns a git repository with one committed Go file.
func repo(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	git(t, root, "init", "-q")
	write(t, root, "a.go", "package a\n")
	write(t, root, "sub/b.go", "package sub\n")
	git(t, root, "add", ".")
	git(t, root, "commit", "-qm", "init")

	return root
}

// result is the hook output the tests read back.
type result struct {
	Decision      string
	Reason        string
	SystemMessage string
	Specific      struct {
		EventName          string
		AdditionalContext  string
		PermissionDecision string
		PermissionReason   string
	}
	// raw is stdout exactly as written; empty means the hook was silent.
	raw string
	// specificKeys is every key the event-specific object carried.
	specificKeys []string
}

// parse reads stdout the way Claude Code does: one JSON object, under the
// harness's own camelCase keys.
func parse(t *testing.T, stdout string) result {
	t.Helper()

	res := result{raw: stdout}
	if stdout == "" {
		return res
	}

	var wire map[string]json.RawMessage

	require.NoError(t, json.Unmarshal([]byte(stdout), &wire), "stdout must be one JSON object: %q", stdout)

	specific := map[string]string{}
	if raw, ok := wire["hookSpecificOutput"]; ok {
		require.NoError(t, json.Unmarshal(raw, &specific))
	}

	res.Specific.EventName = specific["hookEventName"]
	res.Specific.AdditionalContext = specific["additionalContext"]
	res.Specific.PermissionDecision = specific["permissionDecision"]
	res.Specific.PermissionReason = specific["permissionDecisionReason"]

	for key := range specific {
		res.specificKeys = append(res.specificKeys, key)
	}

	for key, into := range map[string]*string{
		"decision": &res.Decision, "reason": &res.Reason, "systemMessage": &res.SystemMessage,
	} {
		if raw, ok := wire[key]; ok {
			require.NoError(t, json.Unmarshal(raw, into))
		}
	}

	return res
}

// fire sends one event and returns what the handler wrote.
func fire(t *testing.T, h *hook.Handler, fields map[string]any) (result, error) {
	t.Helper()

	in, err := json.Marshal(fields)
	require.NoError(t, err)

	var out bytes.Buffer

	if err := h.Handle(t.Context(), bytes.NewReader(in), &out); err != nil {
		return parse(t, out.String()), fmt.Errorf("handling %s: %w", fields["hook_event_name"], err)
	}

	return parse(t, out.String()), nil
}

func edit(root, tool, path string) map[string]any {
	key := "file_path"
	if tool == "NotebookEdit" {
		key = "notebook_path"
	}

	return map[string]any{
		"session_id": sessionID, "cwd": root, "hook_event_name": "PostToolUse",
		"tool_name": tool, "tool_input": map[string]any{key: path, "content": "x"},
		"tool_response": map[string]any{"filePath": path}, "an_unknown_field": 1,
	}
}

func stop(root string, active bool) map[string]any {
	return map[string]any{
		"session_id": sessionID, "cwd": root, "hook_event_name": "Stop",
		"stop_hook_active": active, "last_assistant_message": "done", "background_tasks": []any{},
	}
}

func state(t *testing.T, root string) session.State {
	t.Helper()

	st, err := session.NewStore(root).Update(t.Context(), sessionID, func(*session.State) error { return nil })
	require.NoError(t, err)

	return st
}

func TestHandle_PostToolUse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		tool    string
		path    string // relative to the root unless absolute
		verdict hook.Verdict
		// wantRun is the file the edit stage is run on, "" for no run.
		wantRun      string
		wantContext  string
		wantRecorded []string
	}{
		{
			name: "a clean edit is silent", tool: "Edit", path: "a.go",
			wantRun: "a.go", wantRecorded: []string{"a.go"},
		},
		{
			name: "what the checks render becomes context for the model", tool: "Write", path: "sub/b.go",
			verdict: hook.Verdict{Text: "gofmt rewrote sub/b.go; re-read it"},
			wantRun: "sub/b.go", wantContext: "gofmt rewrote sub/b.go; re-read it", wantRecorded: []string{"sub/b.go"},
		},
		{
			name: "a failing edit check informs and never blocks", tool: "MultiEdit", path: "a.go",
			verdict: hook.Verdict{Failed: true, Fingerprint: "f", Text: "a.go:1: syntax error"},
			wantRun: "a.go", wantContext: "a.go:1: syntax error", wantRecorded: []string{"a.go"},
		},
		{
			name: "a notebook edit names its file differently", tool: "NotebookEdit", path: "nb.ipynb",
			wantRun: "nb.ipynb", wantRecorded: []string{"nb.ipynb"},
		},
		{
			name: "a file outside the project is not the project's to check", tool: "Write",
			path: filepath.Join(os.TempDir(), "elsewhere", "plan.md"), wantRecorded: []string{},
		},
		{
			name: "a tool that edits nothing is ignored", tool: "Bash", path: "a.go", wantRecorded: nil,
		},
		{
			name: "an edit without a path is ignored", tool: "Edit", path: "", wantRecorded: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := repo(t)
			runner := &fakeRunner{verdicts: []hook.Verdict{tt.verdict}}

			path := tt.path
			if path != "" && !filepath.IsAbs(path) {
				path = filepath.Join(root, filepath.FromSlash(path))
			}

			res, err := fire(t, hook.New(runner), edit(root, tt.tool, path))
			require.NoError(t, err)

			if tt.wantRun == "" {
				assert.Empty(t, runner.calls)
			} else {
				assert.Equal(t, []call{{stage: gate.StageEdit, changed: []string{tt.wantRun}}}, runner.calls)
			}

			assert.Empty(t, res.Decision, "PostToolUse never blocks")
			assert.Equal(t, tt.wantContext, res.Specific.AdditionalContext)

			if tt.wantContext == "" {
				assert.Empty(t, res.raw)
			} else {
				assert.Equal(t, "PostToolUse", res.Specific.EventName)
			}

			if tt.wantRecorded == nil {
				assert.NoDirExists(t, filepath.Join(root, ".wavez"))

				return
			}

			assert.Equal(t, tt.wantRecorded, state(t, root).Recorded)
		})
	}
}

func TestHandle_PostToolUse_FileNamedThroughASymlinkedRoot(t *testing.T) {
	t.Parallel()

	root := repo(t)
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(root, link))

	runner := &fakeRunner{}

	_, err := fire(t, hook.New(runner), edit(root, "Edit", filepath.Join(link, "a.go")))
	require.NoError(t, err)
	assert.Equal(t, []call{{stage: gate.StageEdit, changed: []string{"a.go"}}}, runner.calls)
}

func TestHandle_RunnerErrorIsNotABlock(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"PostToolUse", "Stop"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			root := repo(t)
			h := hook.New(&fakeRunner{err: errRunner})

			start(t, h, root)
			write(t, root, "a.go", "package a // changed\n")

			fields := stop(root, false)
			if name == "PostToolUse" {
				fields = edit(root, "Edit", filepath.Join(root, "a.go"))
			}

			res, err := fire(t, h, fields)
			require.ErrorIs(t, err, errRunner)
			assert.Empty(t, res.raw, "an error must leave stdout empty, or the harness would read a decision from it")
		})
	}
}

func TestHandle_MalformedStdin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		stdin string
	}{
		{name: "empty", stdin: ""},
		{name: "not JSON", stdin: "Stop please"},
		{name: "truncated", stdin: `{"hook_event_name": "Stop", "cwd": "/tmp`},
		{name: "wrong type", stdin: `{"hook_event_name": "Stop", "cwd": 7}`},
		{name: "an array", stdin: `[]`},
		{name: "no event name", stdin: `{"session_id": "s", "cwd": "/tmp"}`},
		{name: "no cwd", stdin: `{"session_id": "s", "hook_event_name": "Stop"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			runner := &fakeRunner{}

			var out bytes.Buffer

			err := hook.New(runner).Handle(t.Context(), strings.NewReader(tt.stdin), &out)
			require.ErrorIs(t, err, hook.ErrMalformedEvent)
			assert.Empty(t, out.String())
			assert.Empty(t, runner.calls)
		})
	}
}

func TestHandle_SessionWithoutAnID(t *testing.T) {
	t.Parallel()

	fields := stop(repo(t), false)
	fields["session_id"] = "../../escape"

	_, err := fire(t, hook.New(&fakeRunner{}), fields)
	require.ErrorIs(t, err, session.ErrInvalidID)
}

func TestHandle_OtherEvents(t *testing.T) {
	t.Parallel()

	t.Run("SessionStart takes the baseline and says nothing", func(t *testing.T) {
		t.Parallel()

		root := repo(t)
		write(t, root, "wip.txt", "from before\n")

		runner := &fakeRunner{}

		res, err := fire(t, hook.New(runner), map[string]any{
			"session_id": sessionID, "cwd": root, "hook_event_name": "SessionStart", "source": "startup",
		})
		require.NoError(t, err)
		assert.Empty(t, res.raw)
		assert.Empty(t, runner.calls)

		st := state(t, root)
		assert.True(t, st.Baseline.Git)
		assert.Contains(t, st.Baseline.Dirty, "wip.txt")
	})

	t.Run("an event wavez has no part in is ignored", func(t *testing.T) {
		t.Parallel()

		root := repo(t)

		res, err := fire(t, hook.New(&fakeRunner{}), map[string]any{
			"session_id": sessionID, "cwd": root, "hook_event_name": "PreCompact",
		})
		require.NoError(t, err)
		assert.Empty(t, res.raw)
		assert.NoDirExists(t, filepath.Join(root, ".wavez"))
	})
}

func TestHandle_Trace(t *testing.T) {
	t.Parallel()

	root := repo(t)

	var trace bytes.Buffer

	runner := &fakeRunner{verdicts: []hook.Verdict{{Failed: true, Fingerprint: "f", Text: "red"}}}
	h := hook.New(runner, hook.WithTrace(&trace))

	_, err := fire(t, h, edit(root, "Edit", filepath.Join(root, "a.go")))
	require.NoError(t, err)

	_, err = fire(t, h, stop(root, false))
	require.NoError(t, err)

	err = h.Handle(t.Context(), strings.NewReader("{"), &bytes.Buffer{})
	require.Error(t, err)

	lines := strings.Split(strings.TrimSpace(trace.String()), "\n")
	require.Len(t, lines, 2, "an event that could not be read has nothing to trace")
	assert.JSONEq(t, `{"event":"PostToolUse","session":"`+sessionID+`","root":"`+root+
		`","tool":"Edit","changed":["a.go"],"outcome":"context","agent_bytes":3}`, lines[0])
	assert.JSONEq(t, `{"event":"Stop","session":"`+sessionID+`","root":"`+root+
		`","changed":["a.go"],"outcome":"block","agent_bytes":3}`, lines[1])
}

type stubGate struct {
	err error
}

func (s stubGate) Run(context.Context, string, []string, gate.Stage) (gate.Report, error) {
	return gate.Report{}, s.err
}

func TestGate_Run(t *testing.T) {
	t.Parallel()

	verdict, err := hook.Gate{Runner: stubGate{}}.Run(t.Context(), ".", nil, gate.StageStop)
	require.NoError(t, err)
	assert.Equal(t, hook.Verdict{}, verdict, "an empty report fails nothing and says nothing")

	_, err = hook.Gate{Runner: stubGate{err: errRunner}}.Run(t.Context(), ".", nil, gate.StageStop)
	require.ErrorIs(t, err, errRunner)
}
