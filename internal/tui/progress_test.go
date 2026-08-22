package tui_test

import (
	"strings"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/tui"
)

// The progress line reports the turn and never the run: what a run has left
// is not predictable from anything on disk, and a thread that is not
// working has no turn in flight to report.
func TestThreadProgressLineReportsTheTurnAndOnlyWhileWorking(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 22, 10, 0, 30, 0, time.UTC)

	working := api.ThreadInfo{
		ID: "t1", Name: "fix-lock", Root: "/p", Dir: "/p", Step: "str_replace",
		State: event.StateWorking, Turn: 4, TurnStart: now.Add(-12 * time.Second),
		TurnMean: 9 * time.Second,
	}

	tests := []struct {
		name string
		want []string
		gone []string
		info api.ThreadInfo
	}{
		{
			name: "a working turn shows how long it has run against this run's mean",
			info: working,
			want: []string{"turn 4", "12s", "~9s"},
			gone: []string{"remaining", "left"},
		},
		{
			name: "the first turn of a run has no mean yet and says only how long it has run",
			info: func() api.ThreadInfo { i := working; i.Turn, i.TurnMean = 1, 0; return i }(),
			want: []string{"turn 1", "12s"},
			gone: []string{"~"},
		},
		{
			name: "an idle thread shows no progress line at all",
			info: func() api.ThreadInfo { i := working; i.State = event.StateIdle; return i }(),
			gone: []string{"turn 4", "~9s"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newSized(t, tui.Options{NoColor: true, Now: func() time.Time { return now }}, 100, 24)
			got := openThread(t, m, []api.ThreadInfo{tt.info}).View().Content

			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("view is missing %q:\n%s", want, got)
				}
			}

			for _, gone := range tt.gone {
				if strings.Contains(got, gone) {
					t.Errorf("view should not contain %q:\n%s", gone, got)
				}
			}
		})
	}
}
