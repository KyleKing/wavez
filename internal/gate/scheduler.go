package gate

import (
	"context"
	"sort"
	"sync"
)

// RunGates executes gates against rc, serializing any pair that shares a
// resource key (DESIGN.md's Gates section: "Gates sharing a resource
// serialize, others run in parallel") and running every other pair
// concurrently. Results are returned in the same order as gates,
// regardless of completion order. Resource locks are always acquired in
// sorted key order across every gate, which is what keeps two gates with
// overlapping-but-different resource sets from deadlocking each other.
func RunGates(ctx context.Context, clock Clock, gates []Gate, rc RunContext) []Result {
	locks := resourceLocks(gates)
	results := make([]Result, len(gates))

	var wg sync.WaitGroup

	for i, g := range gates {
		wg.Add(1)

		go func(i int, g Gate) {
			defer wg.Done()

			held := locksFor(locks, g.Resources())
			for _, l := range held {
				l.Lock()
			}
			defer func() {
				for _, l := range held {
					l.Unlock()
				}
			}()

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

func resourceLocks(gates []Gate) map[string]*sync.Mutex {
	locks := make(map[string]*sync.Mutex)

	for _, g := range gates {
		for _, r := range g.Resources() {
			if _, ok := locks[r]; !ok {
				locks[r] = &sync.Mutex{}
			}
		}
	}

	return locks
}

func locksFor(all map[string]*sync.Mutex, keys []string) []*sync.Mutex {
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)

	out := make([]*sync.Mutex, 0, len(sorted))
	for _, k := range sorted {
		out = append(out, all[k])
	}

	return out
}
