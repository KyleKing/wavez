package gate_test

import (
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/gate"
)

func TestNeedsFullRun(t *testing.T) {
	t.Parallel()

	cfg := gate.CadenceConfig{MaxSelectivePasses: 5, MaxInterval: time.Hour}

	tests := []struct {
		name            string
		passesSinceFull int
		sinceLastFull   time.Duration
		untrackedFile   bool
		want            bool
	}{
		{name: "well within both bounds", passesSinceFull: 1, sinceLastFull: time.Minute, want: false},
		{name: "untracked file forces a full run", untrackedFile: true, want: true},
		{name: "pass count at threshold", passesSinceFull: 5, sinceLastFull: time.Minute, want: true},
		{name: "time threshold reached", passesSinceFull: 1, sinceLastFull: time.Hour, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := gate.NeedsFullRun(cfg, tt.passesSinceFull, tt.sinceLastFull, tt.untrackedFile)
			if got != tt.want {
				t.Errorf("NeedsFullRun(%+v, %d, %s, %v) = %v, want %v",
					cfg, tt.passesSinceFull, tt.sinceLastFull, tt.untrackedFile, got, tt.want)
			}
		})
	}
}
