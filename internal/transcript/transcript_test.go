package transcript_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/transcript"
)

var update = flag.Bool("update", false, "rewrite the golden frames")

// Every fixture in testdata replays against the real loop and tool surface,
// and its frame is the wording the harness hands a model. It is the cheap
// half of the dogfood loop: a replay lane costs three minutes on an idle
// laptop and answers a question about the model, where this costs a second
// and answers one about the harness.
func compare(t *testing.T, golden, got string) {
	t.Helper()

	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
			t.Fatalf("writing golden: %v", err)
		}

		return
	}

	want, err := os.ReadFile(golden) //nolint:gosec // golden is derived from a path this test globbed
	if err != nil {
		t.Fatalf("reading golden (run with -update to write it): %v", err)
	}

	if got != string(want) {
		t.Errorf("frame changed:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestFixturesRenderTheSameFrame(t *testing.T) {
	t.Parallel()

	fixtures, err := filepath.Glob(filepath.Join("testdata", "*.json"))
	if err != nil {
		t.Fatalf("globbing fixtures: %v", err)
	}

	if len(fixtures) == 0 {
		t.Fatal("no fixtures, so this test asserts nothing")
	}

	for _, path := range fixtures {
		t.Run(strings.TrimSuffix(filepath.Base(path), ".json"), func(t *testing.T) {
			t.Parallel()

			fixture, err := transcript.Load(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			got, err := transcript.Replay(t.Context(), fixture, t.TempDir(), t.TempDir())
			if err != nil {
				t.Fatalf("Replay: %v", err)
			}

			compare(t, strings.TrimSuffix(path, ".json")+".golden", got)
		})
	}
}
