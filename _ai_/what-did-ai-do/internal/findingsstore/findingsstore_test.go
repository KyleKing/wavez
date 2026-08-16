package findingsstore_test

import (
	"testing"
	"time"

	"github.com/kyleking/what-did-ai-do/internal/adversarial"
	"github.com/kyleking/what-did-ai-do/internal/decision"
	"github.com/kyleking/what-did-ai-do/internal/findingsstore"
	"github.com/kyleking/what-did-ai-do/internal/llm"
)

// isolateCacheDir points os.UserCacheDir at a temp directory. UserCacheDir
// consults $XDG_CACHE_HOME on Linux but $HOME/Library/Caches on darwin, so
// both must be overridden to isolate every platform's real user cache.
func isolateCacheDir(t *testing.T) {
	t.Helper()

	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CACHE_HOME", dir)
}

//nolint:paralleltest // isolateCacheDir uses t.Setenv, which forbids t.Parallel
func TestSaveLoad_RoundTrips(t *testing.T) {
	isolateCacheDir(t)

	want := findingsstore.Report{
		SessionID: "sess-roundtrip",
		Analyzed:  3,
		Findings: []findingsstore.Finding{
			{
				DecisionID: "sess-roundtrip-000",
				Summary:    "ran Bash: rm -rf x",
				Assessment: "slop",
				Category:   "own-choice",
				Confidence: 0.9,
				Concern:    "destructive",
			},
		},
		AnalyzedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}

	if err := findingsstore.Save(want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, found, err := findingsstore.Load("sess-roundtrip")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if !found {
		t.Fatal("Load() found = false, want true")
	}

	if got.SessionID != want.SessionID || got.Analyzed != want.Analyzed || len(got.Findings) != len(want.Findings) {
		t.Errorf("Load() = %+v, want %+v", got, want)
	}
}

//nolint:paralleltest // isolateCacheDir uses t.Setenv, which forbids t.Parallel
func TestLoad_NotFound(t *testing.T) {
	isolateCacheDir(t)

	_, found, err := findingsstore.Load("sess-never-saved")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if found {
		t.Error("Load() found = true, want false for a session that was never saved")
	}
}

func TestFromAnalyzer_ConvertsFindings(t *testing.T) {
	t.Parallel()

	report := adversarial.Report{
		SessionID: "sess-convert",
		Analyzed:  2,
		Findings: []adversarial.Finding{
			{
				Candidate: adversarial.Candidate{
					Decision: decision.Decision{Summary: "edited foo.go"},
				},
				Judgment: llm.Judgment{
					DecisionID: "sess-convert-000",
					Assessment: "questionable",
					Category:   "unconsidered-alternative",
					Confidence: 0.75,
					Concern:    "a simpler approach existed",
					Suggestion: "use a map for dedup",
				},
			},
		},
	}

	at := time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC)

	got := findingsstore.FromAnalyzer(report, at)

	if got.SessionID != "sess-convert" || got.Analyzed != 2 || !got.AnalyzedAt.Equal(at) {
		t.Errorf("FromAnalyzer() metadata = %+v", got)
	}

	if len(got.Findings) != 1 {
		t.Fatalf("len(Findings) = %d, want 1", len(got.Findings))
	}

	f := got.Findings[0]
	if f.DecisionID != "sess-convert-000" || f.Summary != "edited foo.go" || f.Suggestion != "use a map for dedup" {
		t.Errorf("Findings[0] = %+v", f)
	}
}
