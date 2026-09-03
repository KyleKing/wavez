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
	result, err := sandbox.Exec(ctx, root, os.TempDir(), "sh", "-c", command)
	if err != nil {
		return fmt.Errorf("running %q: %w", command, err)
	}

	output := result.Stdout + result.Stderr

	tasks := fanout.FromFindings(output)
	if len(tasks) == 0 {
		fmt.Fprintf(os.Stderr, "%q reported no findings with a file and line to split on\n", command)

		return nil
	}

	split, err := fanout.Split(tasks, lanes)
	if err != nil {
		return fmt.Errorf("splitting %d files into %d lanes: %w", len(tasks), lanes, err)
	}

	if err := fanout.Check(split); err != nil {
		return fmt.Errorf("verifying the split: %w", err)
	}

	printLanes(command, tasks, split)

	return nil
}

func printLanes(command string, tasks []fanout.Task, lanes []fanout.Lane) {
	total := 0
	for _, t := range tasks {
		total += t.Weight
	}

	fmt.Printf("%d findings across %d files, split into %d disjoint lanes\n\n",
		total, len(tasks), len(lanes))

	for i, lane := range lanes {
		prompt := fmt.Sprintf("Run `%s` and fix every finding it reports in these files, "+
			"changing nothing else: %s", command, strings.Join(lane.Paths, " "))

		fmt.Printf("lane %d  %d findings in %d files\n", i+1, lane.Weight, len(lane.Tasks))
		fmt.Printf("  %s\n", strings.Join(lane.Paths, " "))
		fmt.Printf("  wavez -p %s\n\n", shellQuote(prompt))
	}
}

// shellQuote wraps s so a shell reads it as one word, whatever it holds. The
// printed lane is meant to be pasted, and a check command carrying a quote
// would otherwise print a line that runs as something else.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
