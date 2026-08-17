package app_test

import (
	"context"
	"os"
	"testing"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/app"
	"github.com/kyleking/wavez/internal/llm/openaic"
	"github.com/kyleking/wavez/internal/tool"
)

// The task that motivated the review step: two strings, one condition, and a
// diff that satisfies every deterministic check while doing the wrong thing.
const (
	emptyStateTask = `The thread list's empty state always prints "No threads yet". ` +
		`Make it print "No threads match this filter" when a filter is set, ` +
		`and keep printing "No threads yet" when no filter is set.`

	branchedDiff = `diff --git a/internal/tui/home.go b/internal/tui/home.go
--- a/internal/tui/home.go
+++ b/internal/tui/home.go
@@ -40,7 +40,11 @@ func (m model) threadListEmpty() string {
 	if len(m.threads) > 0 {
 		return ""
 	}
-	return "No threads yet"
+	if m.filter != "" {
+		return "No threads match this filter"
+	}
+
+	return "No threads yet"
 }
`

	appendedDiff = `diff --git a/internal/tui/home.go b/internal/tui/home.go
--- a/internal/tui/home.go
+++ b/internal/tui/home.go
@@ -40,7 +40,7 @@ func (m model) threadListEmpty() string {
 	if len(m.threads) > 0 {
 		return ""
 	}
-	return "No threads yet"
+	return "No threads yet" + "\n" + "No threads match this filter"
 }
diff --git a/internal/tui/home_test.go b/internal/tui/home_test.go
--- a/internal/tui/home_test.go
+++ b/internal/tui/home_test.go
@@ -12,4 +12,10 @@ func TestThreadListEmpty(t *testing.T) {
 	if got := m.threadListEmpty(); got == "" {
 		t.Fatal("want an empty-state line")
 	}
+	if !strings.Contains(m.threadListEmpty(), "No threads yet") {
+		t.Error("want the unfiltered line")
+	}
+	if !strings.Contains(m.threadListEmpty(), "No threads match this filter") {
+		t.Error("want the filtered line")
+	}
 }
`

	switchedDiff = `diff --git a/internal/tui/home.go b/internal/tui/home.go
--- a/internal/tui/home.go
+++ b/internal/tui/home.go
@@ -40,7 +40,12 @@ func (m model) threadListEmpty() string {
 	if len(m.threads) > 0 {
 		return ""
 	}
-	return "No threads yet"
+	switch m.filter {
+	case "":
+		return "No threads yet"
+	default:
+		return "No threads match this filter"
+	}
 }
`

	unrelatedDiff = `diff --git a/internal/tui/home.go b/internal/tui/home.go
--- a/internal/tui/home.go
+++ b/internal/tui/home.go
@@ -40,7 +40,7 @@ func (m model) threadListEmpty() string {
-	if len(m.threads) > 0 {
+	if len(m.threads) != 0 {
 		return ""
 	}
 	return "No threads yet"
 }
`
)

type fixedDiffer struct{ diff string }

func (d fixedDiffer) Diff(context.Context, string, string, []string) (string, error) {
	return d.diff, nil
}

// TestModelReviewer_LocalModel asks the real local model to tell the correct
// change from the one that passed every deterministic gate while doing
// something else. It is skipped unless WAVEZ_LLAMA_SERVER_URL names a running
// server, so it never runs in CI.
// One llama-server serves one model at a time, so these subtests share a
// resource and run sequentially rather than in parallel.
func TestModelReviewer_LocalModel(t *testing.T) { //nolint:paralleltest // see the comment above
	baseURL := os.Getenv("WAVEZ_LLAMA_SERVER_URL")
	if baseURL == "" {
		t.Skip("WAVEZ_LLAMA_SERVER_URL not set; skipping the live local-model review test")
	}

	const samples = 3

	tests := []struct {
		name string
		diff string
		want agent.ReviewResult
	}{
		{name: "branched on the filter", diff: branchedDiff, want: agent.ReviewOK},
		{name: "appended beside the old string", diff: appendedDiff, want: agent.ReviewObjection},
		{name: "branched with a switch", diff: switchedDiff, want: agent.ReviewOK},
		{name: "changed something else entirely", diff: unrelatedDiff, want: agent.ReviewObjection},
	}

	for _, tt := range tests { //nolint:paralleltest // one llama-server, one model: these share it
		t.Run(tt.name, func(t *testing.T) {
			local := openaic.New("local", openaic.WithBaseURL(baseURL), openaic.WithModel("qwen3:8b"))
			reviewer := app.NewModelReviewer("/repo", fixedDiffer{diff: tt.diff}, local, nil, "qwen3:8b", "")

			agreed := 0

			for range samples {
				got := reviewer.Review(context.Background(), agent.Review{
					Task:    emptyStateTask,
					Changes: []tool.Change{{Path: "internal/tui/home.go", Added: 4}},
				})
				t.Logf("verdict=%s note=%s", got.Result, got.Note)

				if got.Result == tt.want {
					agreed++
				}
			}

			if agreed <= samples/2 {
				t.Errorf("the local model answered %s %d of %d times", tt.want, agreed, samples)
			}
		})
	}
}
