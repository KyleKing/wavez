package gate

import (
	"context"
	"runtime"
	"sync"
)

// job is one unit of a run that only reads the tree: the Go compile and test
// pair, lint, or one configured check.
type job struct {
	run func(ctx context.Context) []Result
	// exclusive holds every slot while the job runs.
	exclusive bool
}

// slots is how many jobs may run at once: the configured bound, or one per
// CPU.
func slots(configured int) int {
	if configured > 0 {
		return configured
	}

	return runtime.NumCPU()
}

// schedule runs jobs with at most limit of them at a time and returns each
// one's results in the slot of its index, so the report's order does not
// depend on which finished first. Jobs start in index order. An exclusive
// job waits for every running job to finish and holds all limit slots, so
// nothing starts beside it. A job whose turn comes after ctx ended still
// runs, and abstains at once with the reason ctx gives.
func schedule(ctx context.Context, limit int, jobs []job) [][]Result {
	out := make([][]Result, len(jobs))
	sem := make(chan struct{}, limit)

	var wg sync.WaitGroup

	for i, j := range jobs {
		weight := 1
		if j.exclusive {
			weight = limit
		}

		held := acquire(ctx, sem, weight)

		wg.Go(func() {
			defer release(sem, held)

			out[i] = j.run(ctx)
		})
	}

	wg.Wait()

	return out
}

// acquire takes up to n slots, one at a time, and returns how many it took:
// fewer than n only when ctx ended first. One dispatcher takes slots for
// every job in turn, so two jobs never each hold part of what both need.
func acquire(ctx context.Context, sem chan struct{}, n int) int {
	for held := range n {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return held
		}
	}

	return n
}

func release(sem chan struct{}, n int) {
	for range n {
		<-sem
	}
}
