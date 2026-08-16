package daemon

import "github.com/kyleking/wavez/internal/api"

func (s *Server) diagnostics() api.Diagnostics {
	d := api.Diagnostics{
		Threads:      s.mgr.count(),
		NeedsInput:   s.mgr.needsInputCount(),
		GateQueue:    s.broker.gateQueueCount(),
		GateRuns:     s.broker.askedCount(),
		GateFailures: s.broker.deniedCount(),
		ToolCalls:    s.mgr.toolCallCount(),
		Malformed:    s.mgr.malformedCount(),
	}
	if s.stats == nil {
		return d
	}

	m := s.stats.Stats()
	d.MemUsedBytes = m.UsedBytes
	d.MemTotalBytes = m.TotalBytes
	d.ModelBytes = m.ModelBytes

	return d
}
