package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/kyleking/wavez/internal/fanout"
	"github.com/kyleking/wavez/internal/sandbox"
)

// defaultFanoutLanes is what -fanout splits into unasked. Four lanes is
// what the 24 GB laptop admits beside a local model without the scheduler
// holding one of them on memory.
const defaultFanoutLanes = 4

// fanoutPlan runs a check and prints the lanes its findings split into.
//
// It stops at the plan on purpose. The split is the piece that was missing,
// since leases already fence the writes and scoped gate reports already keep
// one lane's failure off another, and a plan a reader can plainly check is
// worth more than a scheduler nobody has verified the split of.
func fanoutPlan(ctx context.Context, root, command string, lanes int) error {
	split, tasks, err := planLanes(ctx, root, command, lanes)
	if err != nil || split == nil {
		return err
	}

	printLanes(command, tasks, split)

	return nil
}

// runCheck runs the check and returns everything it printed, which is what
// the findings are parsed out of. A non-zero exit is the normal case, since
// a check with findings to split reports them by failing.
func runCheck(ctx context.Context, root, command string) (string, error) {
	result, err := sandbox.Exec(ctx, root, os.TempDir(), sandbox.Policy{}, []string{"sh", "-c", command})
	if err != nil {
		return "", fmt.Errorf("running %q: %w", command, err)
	}

	return result.Stdout + result.Stderr, nil
}

// planLanes runs the check and splits its findings. A nil split with a nil
// error is a check that reported nothing to split, which is not a failure.
func planLanes(ctx context.Context, root, command string, lanes int) ([]fanout.Lane, []fanout.Task, error) {
	output, err := runCheck(ctx, root, command)
	if err != nil {
		return nil, nil, err
	}

	tasks := fanout.FromFindings(output)
	if len(tasks) == 0 {
		fmt.Fprintf(os.Stderr, "%q reported no findings with a file and line to split on\n", command)

		return nil, nil, nil
	}

	split, err := fanout.Split(tasks, lanes)
	if err != nil {
		return nil, nil, fmt.Errorf("splitting %d files into %d lanes: %w", len(tasks), lanes, err)
	}

	if err := fanout.Check(split); err != nil {
		return nil, nil, fmt.Errorf("verifying the split: %w", err)
	}

	return split, tasks, nil
}

func printLanes(command string, tasks []fanout.Task, lanes []fanout.Lane) {
	fmt.Print(laneReport(command, tasks, lanes))
}

// laneReport renders the plan. Each lane prints the command that runs it,
// built by the same lanePrompt a -fanout-run lane is given, so the printed
// plan describes what running it actually does.
func laneReport(command string, tasks []fanout.Task, lanes []fanout.Lane) string {
	total := 0
	for _, t := range tasks {
		total += t.Weight
	}

	var b strings.Builder

	fmt.Fprintf(&b, "%d findings across %d files, split into %d disjoint lanes\n\n",
		total, len(tasks), len(lanes))

	for i, lane := range lanes {
		fmt.Fprintf(&b, "lane %d  %d findings in %d files\n", i+1, lane.Weight, len(lane.Tasks))
		fmt.Fprintf(&b, "  %s\n", strings.Join(lane.Paths, " "))
		fmt.Fprintf(&b, "  wavez -p %s\n\n", shellQuote(lanePrompt(command, lane)))
	}

	return b.String()
}

// shellQuote wraps s so a shell reads it as one word, whatever it holds. The
// printed lane is meant to be pasted, and a check command carrying a quote
// would otherwise print a line that runs as something else.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
