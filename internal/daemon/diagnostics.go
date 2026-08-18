package daemon

import "github.com/kyleking/wavez/internal/api"

// unmeasurableGauges are the panel's numbers nothing in wavez observes today.
// Decode speed and local prefix-cache reuse are both llama-server timings the
// OpenAI-compatible stream never carries, and the daemon does not scrape
// llama-server's metrics endpoint, so neither has a source to plumb.
var unmeasurableGauges = []api.Gauge{api.GaugeTokensPerSec, api.GaugePrefixHit}

func (s *Server) diagnostics() api.Diagnostics {
	u := s.mgr.totalUsage()
	counts := s.leases.Counts()

	d := api.Diagnostics{
		LeasesHeld:    counts.Held,
		LeasesWaiting: counts.Waiting,
		LocalModel:    s.mgr.localModel(),
		Threads:       s.mgr.count(),
		NeedsInput:    s.mgr.needsInputCount(),
		GateQueue:     s.broker.gateQueueCount(),
		GateRuns:      s.broker.askedCount(),
		GateFailures:  s.broker.deniedCount(),
		ToolCalls:     s.mgr.toolCallCount(),
		Malformed:     s.mgr.malformedCount(),
		SpendToday:    s.mgr.spend.today(),
		Unmeasured:    append([]api.Gauge(nil), unmeasurableGauges...),
	}

	if u.input > 0 {
		d.CacheRead = float64(u.cacheRead) / float64(u.input)
	} else {
		d.Unmeasured = append(d.Unmeasured, api.GaugeCacheRead)
	}

	if s.stats == nil {
		d.Unmeasured = append(d.Unmeasured, api.GaugeMemory, api.GaugeModelBytes)

		return d
	}

	m := s.stats.Stats()
	d.MemUsedBytes = m.UsedBytes
	d.MemTotalBytes = m.TotalBytes
	d.ModelBytes = m.ModelBytes

	if !m.ModelMeasured {
		d.Unmeasured = append(d.Unmeasured, api.GaugeModelBytes)
	}

	return d
}
