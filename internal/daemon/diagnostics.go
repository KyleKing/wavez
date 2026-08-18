package daemon

import (
	"context"

	"github.com/kyleking/wavez/internal/api"
)

// unmeasurableGauges are the panel's numbers nothing in wavez observes today.
// Each is a subsystem that has not landed or an instrument the loop does not
// keep, and naming them here is what lets a client render a dash instead of
// inferring one from a zero.
//
// Hosted call count and latency need the loop to time and count a turn per
// tier, which it does not. Gate latency and the running gate need the gate
// runner to report its own queue rather than the permission broker standing
// in for it. Escalations need the router to record a tier change on the
// outcome. CPU by process group stops at what
// `ps` can attribute: the TUI is another process the daemon cannot identify,
// and a gate's subprocesses are gone by the time a sample lands.
var unmeasurableGauges = []api.Gauge{
	api.GaugeCPUGates,
	api.GaugeCPUTUI,
	api.GaugeEscalations,
	api.GaugeGateLatency,
	api.GaugeGateRunning,
	api.GaugeHostedCalls,
	api.GaugeHostedLatency,
}

func (s *Server) diagnostics() (api.Diagnostics, error) {
	f, err := s.aggregateFleetStats()
	if err != nil {
		return api.Diagnostics{}, err
	}
	counts := s.aggregateLeaseCounts()

	d := api.Diagnostics{
		LeasesHeld:     counts.Held,
		LeasesWaiting:  counts.Waiting,
		LocalModel:     s.localModel(),
		Threads:        s.aggregateThreadCount(),
		NeedsInput:     f.needsInput,
		GateQueue:      s.broker.gateQueueCount(),
		GateRuns:       s.broker.askedCount(),
		GateFailures:   s.broker.deniedCount(),
		ToolCalls:      s.aggregateToolCalls(),
		Malformed:      s.aggregateMalformed(),
		SpendToday:     s.aggregateSpendToday(),
		ContextUsed:    f.context,
		ContextWindow:  f.window,
		TranscriptRows: f.rows,
		CompactionRuns: f.compactionRuns,
		TokensSaved:    f.tokensSaved,
		EventsPerSec:   s.window.rate(f.rows),
		PerThread:      f.perThread,
		Sparks:         s.window.sparks(),
		Unmeasured:     append([]api.Gauge(nil), unmeasurableGauges...),
	}

	applyUsage(&d, f)
	s.applyMachine(&d)
	s.applyModelDisk(&d)

	return d, nil
}

// applyUsage fills the local and hosted rows from what the threads reported.
// Decode speed and prefix reuse come from the serving runtime's own timings,
// so a fleet that has run no local turn yet has neither.
func applyUsage(d *api.Diagnostics, f fleetStats) {
	if f.usage.input > 0 {
		d.CacheRead = float64(f.usage.cacheRead) / float64(f.usage.input)
	} else {
		d.Unmeasured = append(d.Unmeasured, api.GaugeCacheRead)
	}

	if f.context == 0 {
		d.Unmeasured = append(d.Unmeasured, api.GaugeContext)
	}

	if f.timings == nil {
		d.Unmeasured = append(d.Unmeasured, api.GaugeTokensPerSec, api.GaugePrefixHit)

		return
	}

	d.TokensPerSec = f.timings.DecodePerSecond
	d.PrefixHit = f.timings.PrefixHit()
}

func (s *Server) applyMachine(d *api.Diagnostics) {
	if s.stats == nil {
		d.Unmeasured = append(d.Unmeasured,
			api.GaugeMemory, api.GaugeModelBytes, api.GaugeCPU, api.GaugeCPUDaemon, api.GaugeCPUModel)

		return
	}

	m := s.stats.Stats()
	d.MemUsedBytes = m.UsedBytes
	d.MemTotalBytes = m.TotalBytes
	d.ModelBytes = m.ModelBytes
	d.CPUPercent = m.CPUPercent
	d.CPUDaemon = m.CPUDaemon
	d.CPUModel = m.CPUModel

	if !m.ModelMeasured {
		d.Unmeasured = append(d.Unmeasured, api.GaugeModelBytes)
	}
	if !m.CPUMeasured {
		d.Unmeasured = append(d.Unmeasured, api.GaugeCPU, api.GaugeCPUDaemon, api.GaugeCPUModel)
	}
}

// applyModelDisk totals what the models on disk take, which bounds what the
// router may choose the same way memory does.
func (s *Server) applyModelDisk(d *api.Diagnostics) {
	if s.modelStore == nil {
		d.Unmeasured = append(d.Unmeasured, api.GaugeModelDisk)

		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), modelListTimeout)
	defer cancel()

	models, err := s.modelStore.List(ctx)
	if err != nil {
		d.Unmeasured = append(d.Unmeasured, api.GaugeModelDisk)

		return
	}

	for _, m := range models {
		d.ModelDiskBytes += m.SizeBytes
	}
}
