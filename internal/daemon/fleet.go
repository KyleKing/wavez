package daemon

import (
	"fmt"
	"sort"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/lease"
)

// listThreads answers root's threads, or every loaded project's when root
// is empty. Root names a project already loaded, never one Loader would
// still have to fetch: listing has no business paying to load a project
// nobody has created a thread in yet, so an unloaded root answers empty
// rather than an error.
func (s *Server) listThreads(root string) ([]api.ThreadInfo, error) {
	if root != "" {
		p, ok := s.lookupProject(root)
		if !ok {
			return nil, nil
		}

		return threadsForProject(p)
	}

	var out []api.ThreadInfo
	for _, p := range s.projectsSnapshot() {
		infos, err := threadsForProject(p)
		if err != nil {
			return nil, err
		}
		out = append(out, infos...)
	}

	return out, nil
}

func threadsForProject(p *Project) ([]api.ThreadInfo, error) {
	infos, err := p.mgr.list()
	if err != nil {
		return nil, fmt.Errorf("listing threads in %s: %w", p.root, err)
	}
	for i := range infos {
		infos[i].Root = p.root
	}

	return infos, nil
}

// aggregateFleetStats merges every loaded project's fleetStats: sums add,
// and context/window/timings (which describe one thread, not a fleet) come
// from whichever project's reading is the most recent.
func (s *Server) aggregateFleetStats() (fleetStats, error) {
	var out fleetStats

	for _, p := range s.projectsSnapshot() {
		f, err := p.mgr.fleetStats()
		if err != nil {
			return fleetStats{}, fmt.Errorf("gathering stats for %s: %w", p.root, err)
		}

		out.usage.input += f.usage.input
		out.usage.output += f.usage.output
		out.usage.cacheRead += f.usage.cacheRead
		out.rows += f.rows
		out.needsInput += f.needsInput
		out.compactionRuns += f.compactionRuns
		out.tokensSaved += f.tokensSaved
		out.perThread = append(out.perThread, f.perThread...)

		if !f.latestAt.Before(out.latestAt) {
			out.latestAt = f.latestAt
			if f.context > 0 {
				out.context, out.window = f.context, f.window
			}
			if f.timings != nil {
				out.timings = f.timings
			}
		}
	}

	sort.Slice(out.perThread, func(i, j int) bool { return out.perThread[i].Name < out.perThread[j].Name })

	return out, nil
}

// aggregateThreadCount, aggregateToolCalls, aggregateMalformed, and
// aggregateSpendToday sum one counter across every loaded project, for the
// diagnostics panel's lifetime totals.
func (s *Server) aggregateThreadCount() int {
	n := 0
	for _, p := range s.projectsSnapshot() {
		n += p.mgr.count()
	}

	return n
}

func (s *Server) aggregateToolCalls() int {
	n := 0
	for _, p := range s.projectsSnapshot() {
		n += p.mgr.toolCallCount()
	}

	return n
}

func (s *Server) aggregateMalformed() int {
	n := 0
	for _, p := range s.projectsSnapshot() {
		n += p.mgr.malformedCount()
	}

	return n
}

func (s *Server) aggregateSpendToday() float64 {
	var total float64
	for _, p := range s.projectsSnapshot() {
		total += p.mgr.spend.today()
	}

	return total
}

// aggregateLeaseCounts sums every loaded project's own lease.Manager, since
// a lease is scoped to one project's directory tree.
func (s *Server) aggregateLeaseCounts() lease.Counts {
	var out lease.Counts
	for _, p := range s.projectsSnapshot() {
		c := p.leases.Counts()
		out.Held += c.Held
		out.Waiting += c.Waiting
	}

	return out
}

// localModel names the local model any loaded project's loop is configured
// with. Every project a daemon serves runs against the same laptop's
// llama-server, so the first loaded project to report one is enough.
func (s *Server) localModel() string {
	for _, p := range s.projectsSnapshot() {
		if m := p.mgr.fastModel(); m != "" {
			return m
		}
	}

	return ""
}
