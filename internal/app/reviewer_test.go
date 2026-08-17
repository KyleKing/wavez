package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/app"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/llm/fake"
	"github.com/kyleking/wavez/internal/tool"
)

var errNoRepo = errors.New("not a jj repository")

type stubDiffer struct {
	diff  string
	err   error
	files []string
}

func (d *stubDiffer) Diff(_ context.Context, _, _ string, files []string) (string, error) {
	d.files = files

	return d.diff, d.err
}

func jsonTurn(body string) fake.Turn {
	return fake.Turn{Text: []string{body}, StopReason: llm.StopEndTurn}
}

func review(task string, paths ...string) agent.Review {
	changes := make([]tool.Change, len(paths))
	for i, p := range paths {
		changes[i] = tool.Change{Path: p, Added: 1}
	}

	return agent.Review{Task: task, Checkpoint: "op1", Changes: changes}
}

func TestModelReviewer_Review(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		answer     string
		diff       string
		diffErr    error
		wantResult agent.ReviewResult
		wantNote   string
	}{
		{
			name:       "verdict ok",
			answer:     `{"verdict":"ok","reason":""}`,
			diff:       "--- a/a.go\n+++ b/a.go\n+ok\n",
			wantResult: agent.ReviewOK,
		},
		{
			name:       "verdict objection carries the reason",
			answer:     `{"verdict":"objection","reason":"both strings are appended instead of branched"}`,
			diff:       "--- a/a.go\n+++ b/a.go\n+both\n",
			wantResult: agent.ReviewObjection,
			wantNote:   "both strings are appended instead of branched",
		},
		{
			name:       "an object wrapped in prose still decodes",
			answer:     "<think>hm</think>\n{\"verdict\":\"ok\",\"reason\":\"\"}\n",
			diff:       "--- a/a.go\n+++ b/a.go\n+ok\n",
			wantResult: agent.ReviewOK,
		},
		{
			name:       "an off-schema answer is not a pass",
			answer:     "Looks good to me!",
			diff:       "--- a/a.go\n+++ b/a.go\n+ok\n",
			wantResult: agent.ReviewSkipped,
			wantNote:   "off-schema",
		},
		{
			name:       "a diff over the budget is reported unreviewed",
			answer:     `{"verdict":"ok","reason":""}`,
			diff:       strings.Repeat("+ a line of diff\n", 20000),
			wantResult: agent.ReviewSkipped,
			wantNote:   "over the 60000-token review budget",
		},
		{
			name:       "an empty diff is reported unreviewed",
			answer:     `{"verdict":"ok","reason":""}`,
			diff:       "",
			wantResult: agent.ReviewSkipped,
			wantNote:   "empty",
		},
		{
			name:       "an unreadable diff is reported unreviewed",
			answer:     `{"verdict":"ok","reason":""}`,
			diffErr:    errNoRepo,
			wantResult: agent.ReviewSkipped,
			wantNote:   "could not be read",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			local := fake.New("local", jsonTurn(tt.answer))
			hosted := fake.New("hosted")
			differ := &stubDiffer{diff: tt.diff, err: tt.diffErr}
			reviewer := app.NewModelReviewer("/repo", differ, local, hosted, "qwen3:8b", "openai/gpt-5-mini")

			got := reviewer.Review(context.Background(), review("make the empty state read differently", "a.go"))

			if got.Result != tt.wantResult {
				t.Fatalf("Result = %q (note %q), want %q", got.Result, got.Note, tt.wantResult)
			}
			if tt.wantNote != "" && !strings.Contains(got.Note, tt.wantNote) {
				t.Errorf("Note = %q, want it to carry %q", got.Note, tt.wantNote)
			}
			if tt.wantResult == agent.ReviewOK && got.Note != "" {
				t.Errorf("Note = %q, want empty on a pass", got.Note)
			}
		})
	}
}

func TestModelReviewer_PromptCarriesTaskAndDiff(t *testing.T) {
	t.Parallel()

	local := fake.New("local", jsonTurn(`{"verdict":"ok","reason":""}`))
	differ := &stubDiffer{diff: "--- a/empty.go\n+++ b/empty.go\n+no results for that filter\n"}
	reviewer := app.NewModelReviewer("/repo", differ, local, fake.New("hosted"), "qwen3:8b", "openai/gpt-5-mini")

	reviewer.Review(context.Background(), review("branch the empty-state string on the filter", "empty.go", "empty.go"))

	reqs := local.Requests()
	if len(reqs) != 1 {
		t.Fatalf("local Requests = %d, want 1", len(reqs))
	}

	req := reqs[0]
	if req.Model != "qwen3:8b" {
		t.Errorf("Model = %q, want the local model", req.Model)
	}
	if req.ResponseFormat == nil || !strings.Contains(string(req.ResponseFormat.Schema), "objection") {
		t.Errorf("ResponseFormat = %+v, want the verdict schema", req.ResponseFormat)
	}

	prompt := req.Messages[0].Content
	if !strings.Contains(prompt, "branch the empty-state string on the filter") {
		t.Errorf("prompt is missing the task text: %q", prompt)
	}
	if !strings.Contains(prompt, "no results for that filter") {
		t.Errorf("prompt is missing the diff: %q", prompt)
	}
	if got := differ.files; len(got) != 1 || got[0] != "empty.go" {
		t.Errorf("Diff files = %v, want the distinct changed paths", got)
	}
}

// A review is routed on size alone: a source-plus-test change stays local,
// and a diff past the local context budget escalates.
func TestModelReviewer_RoutesOnSizeNotFileCount(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		diff string
		want string
	}{
		{
			name: "two files within the local budget stay local",
			diff: "--- a/a.go\n+++ b/a.go\n+one\n--- a/a_test.go\n+++ b/a_test.go\n+two\n",
			want: "local",
		},
		{
			name: "a diff past the local context budget escalates",
			diff: strings.Repeat("+ a line of diff\n", 3000),
			want: "hosted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			answer := jsonTurn(`{"verdict":"ok","reason":""}`)
			local, hosted := fake.New("local", answer), fake.New("hosted", answer)
			reviewer := app.NewModelReviewer("/repo", &stubDiffer{diff: tt.diff},
				local, hosted, "qwen3:8b", "openai/gpt-5-mini")

			got := reviewer.Review(context.Background(), review("change both", "a.go", "a_test.go"))
			if got.Result != agent.ReviewOK {
				t.Fatalf("Result = %q (%s), want ok", got.Result, got.Note)
			}

			asked, idle := local, hosted
			if tt.want == "hosted" {
				asked, idle = hosted, local
			}

			if len(asked.Requests()) != 1 {
				t.Errorf("%s Requests = %d, want 1", tt.want, len(asked.Requests()))
			}
			if len(idle.Requests()) != 0 {
				t.Errorf("the other tier was asked to review: %+v", idle.Requests())
			}
		})
	}
}
