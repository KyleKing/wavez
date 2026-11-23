package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const reportSession = "11111111-2222-3333-4444-555555555555"

// reportProject makes a project and a home holding one transcript of a
// session that ran in it under home, and returns the project and the transcript.
func reportProject(t *testing.T, home string) (string, string) {
	t.Helper()

	project := filepath.Join(t.TempDir(), "proj")
	require.NoError(t, os.MkdirAll(filepath.Join(project, ".git"), 0o750))

	lines := []string{
		fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"timestamp":"2026-03-01T10:00:00Z",`+
			`"message":{"content":"Tidy the <b>cache</b>"}}`, reportSession, project),
		`{"type":"assistant","timestamp":"2026-03-01T10:00:05Z","message":{"id":"m1","model":"claude-test-1",` +
			`"usage":{"input_tokens":3,"cache_read_input_tokens":90,"output_tokens":7},` +
			`"content":[{"type":"tool_use","id":"t1","name":"Bash",` +
			`"input":{"command":"go test ./... # secret-token"}}]}}`,
	}

	path := filepath.Join(home, ".claude", "projects", "-proj", reportSession+".jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600))

	return project, path
}

func TestReport_WritesThePageForTheLatestSession(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv(hookLogEnv, "")

	project, _ := reportProject(t, home)

	code, out, errOut := invoke(t, "", "report", "--dir", project)
	require.Equal(t, 0, code, errOut)

	want := filepath.Join(project, ".wavez", "reports", reportSession+".html")
	assert.Equal(t, want+"\n", out, "the path is all it prints")

	raw, err := os.ReadFile(want) //nolint:gosec // a file this test had written
	require.NoError(t, err)

	html := string(raw)
	assert.Contains(t, html, "<h1>proj</h1>")
	assert.Contains(t, html, "Tidy the &lt;b&gt;cache&lt;/b&gt;")
	assert.Contains(t, html, "1 whole-project run through the shell")
	assert.Contains(t, html, `<code id="waste-1">wavez init</code>`)
	assert.NotContains(t, html, "data-detail=", "labels only without --full")

	ignore, err := os.ReadFile(filepath.Join(project, ".wavez", ".gitignore")) //nolint:gosec // as above
	require.NoError(t, err)
	assert.Equal(t, "*\n", string(ignore), "a report never shows up as a change")
}

func TestReport_NamedSessionFullAndOut(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv(hookLogEnv, "")

	project, path := reportProject(t, home)
	out := filepath.Join(t.TempDir(), "page.html")

	for _, arg := range []string{reportSession, path} {
		code, stdout, errOut := invoke(t, "", "report", "--dir", project, "--full", "--out", out, arg)
		require.Equal(t, 0, code, errOut)
		assert.Equal(t, out+"\n", stdout)

		raw, err := os.ReadFile(out) //nolint:gosec // a file this test had written
		require.NoError(t, err)
		assert.Contains(t, string(raw), `data-detail="go test ./... # secret-token"`)
		assert.NoDirExists(t, filepath.Join(project, ".wavez"), "a page written elsewhere leaves the project alone")
	}
}

func TestReport_ReadsWavezOwnSessionState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv(hookLogEnv, "")

	project, _ := reportProject(t, home)

	state := filepath.Join(project, ".wavez", "sessions", reportSession+".json")
	require.NoError(t, os.MkdirAll(filepath.Dir(state), 0o750))
	require.NoError(t, os.WriteFile(state, []byte(
		`{"session_id":"`+reportSession+`","version":1,"recorded":["a.go","b.go"],"failure":{"rounds":2}}`,
	), 0o600))

	trace := filepath.Join(t.TempDir(), "trace.jsonl")
	require.NoError(t, os.WriteFile(trace, []byte(
		`{"event":"PreToolUse","session":"`+reportSession+`","outcome":"deny"}`+"\n"+
			`{"event":"PreToolUse","session":"another","outcome":"deny"}`+"\n"+
			`{"event":"UserPromptSubmit","session":"`+reportSession+`","outcome":"context"}`+"\n",
	), 0o600))

	code, out, errOut := invoke(t, "", "report", "--dir", project, "--text", "--trace", trace)
	require.Equal(t, 0, code, errOut)
	assert.Contains(t, out, "The session ended while checks were failing")
	assert.Contains(t, out, "files edited: 2 (as wavez recorded them)")
	assert.Contains(t, out, "text renames denied: 1\n", "another session's denial is not counted")
	assert.Contains(t, out, "prompts given a hint: 1\n")
	assert.Contains(t, out, "say: Do not run go test over the whole project.")
	assert.NoDirExists(t, filepath.Join(project, ".wavez", "reports"), "--text writes no page")
}

func TestReport_Text(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv(hookLogEnv, "")

	project, _ := reportProject(t, home)

	code, out, errOut := invoke(t, "", "report", "--dir", project, "--text")
	require.Equal(t, 0, code, errOut)
	assert.True(t, strings.HasPrefix(out, "proj  session 11111111  2026-03-01 10:00 UTC\n"), out)
	assert.Contains(t, out, "  cost      not recorded\n")
	assert.NotContains(t, out, "NEEDS YOU", "nothing needs the person, so the section is left out")
	assert.Contains(t, out, "\nWHERE IT WENT\n")
	assert.Contains(t, out, "\nWASTE\n  - 1 whole-project run through the shell\n")
	assert.Contains(t, out, "    say: wavez init\n")
	assert.NotContains(t, out, "secret-token")
}

func TestReport_Failures(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv(hookLogEnv, "")

	project, _ := reportProject(t, home)
	elsewhere := t.TempDir()

	tests := []struct {
		name string
		want string
		args []string
		code int
	}{
		{name: "no session ran here", args: []string{"--dir", elsewhere}, code: 1, want: "finding the latest session"},
		{name: "an unknown session", args: []string{"--dir", project, "nope"}, code: 1, want: "no transcript for"},
		{name: "two sessions", args: []string{"a", "b"}, code: 1, want: "usage: wavez report"},
		{name: "an unknown flag", args: []string{"--nope"}, code: usageStatus, want: "flag provided but not defined"},
		{name: "bench as text", args: []string{"--bench", "--text"}, code: 1, want: "wavez bench report"},
	}

	for _, tt := range tests {
		code, out, errOut := invoke(t, "", append([]string{"report"}, tt.args...)...)
		assert.Equal(t, tt.code, code, tt.name)
		assert.Empty(t, out, tt.name)
		assert.Contains(t, errOut, tt.want, tt.name)
	}
}

func TestReport_Bench(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	t.Setenv(hookLogEnv, "")

	project, _ := reportProject(t, home)

	records := filepath.Join(project, "bench", "records.jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(records), 0o750))
	require.NoError(t, os.WriteFile(records, []byte(
		`{"task_id":"h3","arm":"baseline","served":"m","turns":4,"cost_usd":0.1,"wall_ms":16000,"pass":true}`+"\n"+
			`{"task_id":"h3","arm":"wavez","served":"m","turns":6,"cost_usd":0.2,"wall_ms":8000,"pass":false}`+"\n"+
			`{"task_id":"h3","arm":"wavez","served":"m","error":"would not start","pass":false}`+"\n",
	), 0o600))

	code, out, errOut := invoke(t, "", "report", "--bench", "--dir", project)
	require.Equal(t, 0, code, errOut)

	want := filepath.Join(project, ".wavez", "reports", "bench.html")
	assert.Equal(t, want+"\n", out)

	raw, err := os.ReadFile(want) //nolint:gosec // a file this test had written
	require.NoError(t, err)

	html := string(raw)
	assert.Contains(t, html, "<h3>h3</h3>")
	assert.Contains(t, html, `<td class="n pass-all">1 of 1</td>`)
	assert.Contains(t, html, `<td class="n pass-some">0 of 2</td>`)
	assert.Contains(t, html, `<td class="n">2*</td>`, "too few runs to compare is marked")
	assert.Contains(t, html, `$0.200<span class="track"><i style="width: 100.0%"></i></span>`)
	assert.Contains(t, html, `8.0s<span class="track"><i style="width: 50.0%"></i></span>`)
	assert.NotContains(t, html, "ZgotmplZ")
}
