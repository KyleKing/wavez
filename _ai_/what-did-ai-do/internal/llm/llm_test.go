// Package llm tests live in package llm (not llm_test) as an intentional
// exception to this repo's usual external-test-package convention: tests
// construct Client{run: fakeRun} directly to reach the unexported run seam,
// avoiding real subprocess calls to claude (which cost money and require a
// live login).
//
//nolint:testpackage // needs the unexported `run` field on Client
package llm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func envelope(isError bool, result string) string {
	b := struct {
		Type    string `json:"type"`
		Result  string `json:"result"`
		IsError bool   `json:"is_error"`
	}{Type: "result", IsError: isError, Result: result}

	data, err := json.Marshal(b)
	if err != nil {
		panic(err)
	}

	return string(data)
}

func TestJudge_HappyPath(t *testing.T) {
	t.Parallel()

	wantJSON := `[{"decision_id":"d1","assessment":"sound","category":"none","confidence":0.9,"concern":""}]`
	fakeRun := func(_ context.Context, _ []string, _ string) (string, error) {
		return envelope(false, wantJSON), nil
	}

	c := &Client{Model: "claude-haiku-4-5", run: fakeRun}
	got, err := c.Judge(context.Background(), "system", "user", "{}")
	if err != nil {
		t.Fatalf("Judge() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].DecisionID != "d1" || got[0].Assessment != "sound" || got[0].Confidence != 0.9 {
		t.Errorf("got[0] = %+v, unexpected values", got[0])
	}
}

func TestJudge_CodeFenceStripped(t *testing.T) {
	t.Parallel()

	fenced := "```json\n" +
		`[{"decision_id":"d2","assessment":"slop",` +
		`"category":"unconsidered-alternative","confidence":0.5,"concern":"x"}]` +
		"\n```"
	fakeRun := func(_ context.Context, _ []string, _ string) (string, error) {
		return envelope(false, fenced), nil
	}

	c := &Client{run: fakeRun}
	got, err := c.Judge(context.Background(), "system", "user", "{}")
	if err != nil {
		t.Fatalf("Judge() error = %v", err)
	}
	if len(got) != 1 || got[0].DecisionID != "d2" {
		t.Fatalf("got = %+v, want one judgment with decision_id d2", got)
	}
}

func TestJudge_ObjectWrappedArrayUnwrapped(t *testing.T) {
	t.Parallel()

	// Anthropic's tool-calling API requires a --json-schema's root type to
	// be "object", so a schema requesting an array of judgments is
	// typically satisfied as a one-field wrapper object, not a bare array.
	wrapped := `{"judgments":[{"decision_id":"d3","assessment":"slop",` +
		`"category":"own-choice","confidence":0.9,"concern":"x"}]}`
	fakeRun := func(_ context.Context, _ []string, _ string) (string, error) {
		return envelope(false, wrapped), nil
	}

	c := &Client{run: fakeRun}

	got, err := c.Judge(context.Background(), "system", "user", "{}")
	if err != nil {
		t.Fatalf("Judge() error = %v", err)
	}

	if len(got) != 1 || got[0].DecisionID != "d3" {
		t.Fatalf("Judge() = %+v, want one Judgment with DecisionID d3", got)
	}
}

func TestJudge_IsErrorSurfacesResultText(t *testing.T) {
	t.Parallel()

	fakeRun := func(_ context.Context, _ []string, _ string) (string, error) {
		return envelope(true, "Not logged in · Please run /login"), nil
	}

	c := &Client{run: fakeRun}
	_, err := c.Judge(context.Background(), "system", "user", "{}")
	if err == nil {
		t.Fatal("Judge() error = nil, want error")
	}
	if !strings.Contains(err.Error(), "Not logged in") {
		t.Errorf("Judge() error = %v, want to contain 'Not logged in'", err)
	}
}

func TestJudge_MalformedEnvelope(t *testing.T) {
	t.Parallel()

	fakeRun := func(_ context.Context, _ []string, _ string) (string, error) {
		return "not json at all {{{", nil
	}

	c := &Client{run: fakeRun}
	_, err := c.Judge(context.Background(), "system", "user", "{}")
	if err == nil {
		t.Fatal("Judge() error = nil, want error")
	}
}

func TestJudge_ValidJSONWrongShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		result string
	}{
		{"object instead of array", `{"decision_id":"d1"}`},
		{"array of scalars", `[1, 2, 3]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fakeRun := func(_ context.Context, _ []string, _ string) (string, error) {
				return envelope(false, tt.result), nil
			}

			c := &Client{run: fakeRun}
			got, err := c.Judge(context.Background(), "system", "user", "{}")
			if err == nil {
				t.Fatalf("Judge() error = nil, got = %+v, want error", got)
			}
		})
	}
}

func TestJudge_CommandArgs(t *testing.T) {
	t.Parallel()

	var gotArgs []string
	var gotStdin string
	fakeRun := func(_ context.Context, args []string, stdin string) (string, error) {
		gotArgs = args
		gotStdin = stdin

		return envelope(false, "[]"), nil
	}

	c := &Client{Model: "claude-haiku-4-5", run: fakeRun}
	_, err := c.Judge(context.Background(), "sys-prompt", "user-prompt", "schema-str")
	if err != nil {
		t.Fatalf("Judge() error = %v", err)
	}

	if gotStdin != "user-prompt" {
		t.Errorf("stdin = %q, want %q", gotStdin, "user-prompt")
	}

	want := []string{
		"-p",
		"--model", "claude-haiku-4-5",
		"--output-format", "json",
		"--json-schema", "schema-str",
		"--append-system-prompt", "sys-prompt",
		"--dangerously-skip-permissions",
	}
	if len(gotArgs) != len(want) {
		t.Fatalf("args = %v, want %v", gotArgs, want)
	}
	for i := range want {
		if gotArgs[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, gotArgs[i], want[i])
		}
	}
}

func TestNewClient_DefaultsModel(t *testing.T) {
	t.Parallel()

	c := NewClient()
	if c.Model != "claude-haiku-4-5" {
		t.Errorf("Model = %q, want %q", c.Model, "claude-haiku-4-5")
	}
	if c.run == nil {
		t.Error("run is nil, want real implementation set")
	}
}
