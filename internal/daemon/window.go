package daemon

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/kyleking/wavez/internal/api"
)

// sampleInterval paces the diagnostics window. The daemon samples on its own
// clock rather than when a client asks, so a sparkline covers wall time
// instead of however often something happened to poll.
const sampleInterval = 2 * time.Second

// maxSamples bounds each series to the last few minutes at sampleInterval.
const maxSamples = 90

// sampleWindow is the diagnostics panel's history: one bounded series per
// gauge, plus the marks the window-scoped rates are computed against. `r`
// resets it, which is why lifetime totals live elsewhere.
type sampleWindow struct {
	now      func() time.Time
	series   map[api.Gauge][]float64
	started  time.Time
	baseRows int
	mu       sync.Mutex
}

func newSampleWindow(now func() time.Time) *sampleWindow {
	if now == nil {
		now = time.Now
	}

	return &sampleWindow{now: now, series: map[api.Gauge][]float64{}, started: now()}
}

// record appends one reading per gauge and drops what has aged out.
func (w *sampleWindow) record(samples map[api.Gauge]float64) {
	w.mu.Lock()
	defer w.mu.Unlock()

	for g, v := range samples {
		w.series[g] = append(w.series[g], v)
		if len(w.series[g]) > maxSamples {
			w.series[g] = w.series[g][len(w.series[g])-maxSamples:]
		}
	}
}

// sparks copies the window's series for the wire. The copy matters: the
// caller marshals it while the sampler keeps appending.
func (w *sampleWindow) sparks() map[api.Gauge][]float64 {
	w.mu.Lock()
	defer w.mu.Unlock()

	if len(w.series) == 0 {
		return nil
	}

	out := make(map[api.Gauge][]float64, len(w.series))
	for g, s := range w.series {
		out[g] = append([]float64(nil), s...)
	}

	return out
}

// rate reports events per second since the window opened, given the current
// total. It answers zero for a window younger than one sample interval,
// where the divisor makes the number meaningless rather than large.
func (w *sampleWindow) rate(rows int) float64 {
	w.mu.Lock()
	defer w.mu.Unlock()

	elapsed := w.now().Sub(w.started).Seconds()
	if elapsed < sampleInterval.Seconds() {
		return 0
	}

	delta := rows - w.baseRows
	if delta <= 0 {
		return 0
	}

	return float64(delta) / elapsed
}

// reset clears the history and re-bases the rates on the current totals,
// which is what `r` on the panel does.
func (w *sampleWindow) reset(rows int) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.series = map[api.Gauge][]float64{}
	w.started = w.now()
	w.baseRows = rows
}

// sample runs the window's own clock until ctx is done.
func (s *Server) sample(ctx context.Context) {
	ticker := time.NewTicker(sampleInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// diagnostics bounds its own calls; the sampler's ctx only stops the loop.
			//nolint:contextcheck // see the comment above
			d, err := s.diagnostics()
			if err != nil {
				slog.Warn("sampling diagnostics", "err", err)

				continue
			}
			s.window.record(sparkSamples(d))
		}
	}
}

// sparkSamples picks the gauges worth a sparkline and drops any the daemon
// reported unmeasured, so a dash never enters a series as a zero.
func sparkSamples(d api.Diagnostics) map[api.Gauge]float64 {
	candidates := map[api.Gauge]float64{
		api.GaugeMemory:       memShare(d),
		api.GaugeCPU:          d.CPUPercent,
		api.GaugeTokensPerSec: d.TokensPerSec,
		api.GaugePrefixHit:    d.PrefixHit,
		api.GaugeCacheRead:    d.CacheRead,
	}

	out := make(map[api.Gauge]float64, len(candidates))
	for g, v := range candidates {
		if d.Measured(g) {
			out[g] = v
		}
	}

	return out
}

func memShare(d api.Diagnostics) float64 {
	if d.MemTotalBytes == 0 {
		return 0
	}

	return float64(d.MemUsedBytes) / float64(d.MemTotalBytes)
}
