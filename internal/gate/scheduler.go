package gate

import (
	"context"
	"sort"
	"sync"
)

// ResourceSet owns one lock per resource key. One set shared by every gate
// run and every background job in a process is what makes DESIGN.md's
// "gates sharing a resource serialize" hold beyond a single batch: the
// change-gate runner, a verification round, and the coverage-map build all
// compete for `go test` on the same machine.
//
// A gate takes a resource exclusively. Background work takes it shared, so
// its own workers proceed together while any gate run preempts them at the
// next unit of work rather than waiting out the whole job.
type ResourceSet struct {
	locks map[string]*sync.RWMutex
	mu    sync.Mutex
}

// NewResourceSet builds an empty ResourceSet. A nil *ResourceSet is a
// working no-op, for callers that own no shared resources.
func NewResourceSet() *ResourceSet {
	return &ResourceSet{locks: make(map[string]*sync.RWMutex)}
}

// Lock takes keys exclusively and returns the release func.
func (s *ResourceSet) Lock(keys []string) func() {
	return s.acquire(keys, false)
}

// LockShared takes keys in the mode background work uses: shared with other
// shared holders, excluded by any gate holding the same key.
func (s *ResourceSet) LockShared(keys []string) func() {
	return s.acquire(keys, true)
}

// acquire takes keys in sorted order, which is what keeps two callers with
// overlapping-but-different key sets from deadlocking each other.
func (s *ResourceSet) acquire(keys []string, shared bool) func() {
	if s == nil || len(keys) == 0 {
		return func() {}
	}

	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)

	held := make([]*sync.RWMutex, 0, len(sorted))

	for _, k := range sorted {
		m := s.mutex(k)
		if shared {
			m.RLock()
		} else {
			m.Lock()
		}

		held = append(held, m)
	}

	return func() {
		for i := len(held) - 1; i >= 0; i-- {
			if shared {
				held[i].RUnlock()
			} else {
				held[i].Unlock()
			}
		}
	}
}

func (s *ResourceSet) mutex(key string) *sync.RWMutex {
	s.mu.Lock()
	defer s.mu.Unlock()

	m, ok := s.locks[key]
	if !ok {
		m = &sync.RWMutex{}
		s.locks[key] = m
	}

	return m
}

// RunGates executes gates against rc, serializing any pair that shares a
// resource key (DESIGN.md's Gates section: "Gates sharing a resource
// serialize, others run in parallel") and running every other pair
// concurrently. Results are returned in the same order as gates,
// regardless of completion order. A nil res serializes within this batch
// only.
func RunGates(ctx context.Context, clock Clock, res *ResourceSet, gates []Gate, rc RunContext) []Result {
	if res == nil {
		res = NewResourceSet()
	}

	results := make([]Result, len(gates))

	var wg sync.WaitGroup

	for i, g := range gates {
		wg.Add(1)

		go func(i int, g Gate) {
			defer wg.Done()

			release := res.Lock(g.Resources())
			defer release()

			results[i] = runOne(ctx, clock, g, rc)
		}(i, g)
	}

	wg.Wait()

	return results
}

func runOne(ctx context.Context, clock Clock, g Gate, rc RunContext) Result {
	start := clock.Now()

	result, err := g.Run(ctx, rc)
	if err != nil {
		result = Result{Gate: g.Name(), Level: rc.Selection.Level, Pass: false}
	}

	result.Timestamp = start
	result.Duration = clock.Now().Sub(start)

	return result
}
