package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/app"
	"github.com/kyleking/wavez/internal/fanout"
	"github.com/kyleking/wavez/internal/lease"
	"github.com/kyleking/wavez/internal/reduce"
	"github.com/kyleking/wavez/internal/router"
	"github.com/kyleking/wavez/internal/thread"
)

// fanoutRunAll plans the lanes and then runs every one of them against this
// project, concurrently, and reports what the check says afterwards.
//
// This is the half the split was missing. Nothing about running lanes needed
// new coordination: the lanes are disjoint by construction, leases fence the
// writes, and the change gate is keyed by writer, so what was left was
// opening a thread per lane and joining the results.
func fanoutRunAll(ctx context.Context, root string, opt options) error {
	lanes, _, err := planLanes(ctx, root, opt.fanoutCheck, opt.fanoutLanes)
	if err != nil || lanes == nil {
		return err
	}

	cfg, err := loadConfig(ctx, root, opt.with)
	if err != nil {
		return err
	}

	a, err := app.New(ctx, root, cfg, permissionGate(opt.allowAll), appOptions(opt)...)
	if err != nil {
		return fmt.Errorf("building project: %w", err)
	}
	//nolint:contextcheck // shutdown must outlive the run's context, as in headlessRun
	defer func() {
		if cerr := a.Close(); cerr != nil {
			fmt.Fprintf(os.Stderr, "wavez: shutdown: %v\n", cerr)
		}
	}()

	return runLanes(ctx, a, opt, lanes, opt.fanoutCheck)
}

// laneResult is one lane's run, as everything a joined report needs about it.
type laneResult struct {
	err     error
	id      thread.ID
	paths   []string
	outcome agent.Outcome
	elapsed time.Duration
	index   int
}

// runLanes runs every lane of a split concurrently against one project, then
// re-runs the check so the report says what the lanes together achieved
// rather than what each of them claimed.
//
// One agent.Loop serves them all, which is what the writer-keyed change gate
// and the lease manager were built for: the lanes are disjoint by
// construction, so no two of them write the same subtree and no lane is
// handed another's gate findings. A lane that fails is reported and does not
// stop the others, since the point of splitting is that they do not depend
// on each other.
func runLanes(ctx context.Context, a *app.App, opt options, lanes []fanout.Lane, command string) error {
	hint, err := routerHint(opt.model)
	if err != nil {
		return err
	}

	results := make([]laneResult, len(lanes))

	var wg sync.WaitGroup

	for i, lane := range lanes {
		wg.Add(1)

		go func() {
			defer wg.Done()

			results[i] = runOneLane(ctx, a, hint, i, lane, command)
		}()
	}

	wg.Wait()

	return reportLanes(ctx, a, results, command)
}

func runOneLane(
	ctx context.Context, a *app.App, hint router.Input,
	index int, lane fanout.Lane, command string,
) laneResult {
	res := laneResult{index: index, paths: lane.Paths}
	started := time.Now()

	th, err := a.OpenThread(laneThreadID(index), []string{a.Root})
	if err != nil {
		res.err = fmt.Errorf("opening a thread for lane %d: %w", index+1, err)

		return res
	}

	res.id = th.ID()
	fmt.Fprintf(os.Stderr, "lane %d  thread %s  %d files\n", index+1, th.ID(), len(lane.Paths))

	outcome, err := a.Loop.Run(
		lease.WithHolder(ctx, string(th.ID())),
		th, prefix(a.SystemPrefix, a.Tools), lanePrompt(command, lane), hint,
	)

	res.outcome, res.err, res.elapsed = outcome, err, time.Since(started)

	return res
}

// laneThreadID names a lane's thread. The index is part of it because the
// generated id is a nanosecond clock reading and several lanes open within
// the same call, which on a coarse clock is one id for two threads and so
// one event log for two runs.
func laneThreadID(index int) thread.ID {
	return threadID("") + thread.ID(fmt.Sprintf("-l%d", index+1))
}

// lanePrompt is the same sentence the printed plan hands a reader, so a lane
// run here and a lane pasted into a shell are the same task.
func lanePrompt(command string, lane fanout.Lane) string {
	return fmt.Sprintf("Run `%s` and fix every finding it reports in these files, "+
		"changing nothing else: %s", command, strings.Join(lane.Paths, " "))
}

// laneStatus says how a lane ended, reading the outcome's own stop reason
// rather than only whether Run returned an error.
//
// Most bounds end a run without an error: a lane whose changes failed
// verification twice stops with StopVerifyFailed and a nil error, and
// reading the error alone reported it as done while its change set had been
// abandoned. A lane that is not complete has to say so, since the whole
// point of the join is to catch a lane that claims more than it did.
func laneStatus(r *laneResult) string {
	switch {
	case r.err != nil:
		return "failed: " + r.err.Error()
	case r.outcome.Turns == 0:
		return "no turns"
	case r.outcome.Stop != agent.StopComplete:
		return "stopped: " + string(r.outcome.Stop)
	case len(r.outcome.FinishFindings) > 0:
		return "finished with " + strings.Join(r.outcome.FinishFindings, "; ")
	}

	return "done"
}

// reportLanes prints each lane's own result and then the check's verdict over
// the whole tree, which is the only number that says whether the split
// worked. A lane reporting success while the joined check still fails is the
// failure this exists to surface.
func reportLanes(ctx context.Context, a *app.App, results []laneResult, command string) error {
	fmt.Printf("\n%d lanes\n\n", len(results))

	for i := range results {
		r := &results[i]

		fmt.Printf("lane %d  %s  %d turns  %s  %s\n",
			r.index+1, r.id, r.outcome.Turns, r.elapsed.Round(time.Second), laneStatus(r))
	}

	after, err := checkFindings(ctx, a.Root, command)
	if err != nil {
		return err
	}

	fmt.Printf("\n%s now reports %d findings across %d files\n",
		command, totalFindings(after), len(after))

	return nil
}

func totalFindings(sites map[string]int) int {
	total := 0
	for _, n := range sites {
		total += n
	}

	return total
}

// checkFindings runs the check and counts what it reports per file.
func checkFindings(ctx context.Context, root, command string) (map[string]int, error) {
	output, err := runCheck(ctx, root, command)
	if err != nil {
		return nil, err
	}

	return reduce.Sites(output), nil
}
