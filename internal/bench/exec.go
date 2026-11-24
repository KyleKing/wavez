package bench

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"
)

// invocation is one child process: where it runs, what it runs, and the
// environment it gets. A nil Env inherits this process's.
type invocation struct {
	Dir  string
	Name string
	Args []string
	Env  []string
}

// killGrace is how long a canceled child and its descendants have to exit
// after SIGTERM before they are killed and their pipes are abandoned.
const killGrace = 5 * time.Second

// killSettle is how long a killed process group is waited for before the
// run returns without knowing it is gone.
const killSettle = 2 * time.Second

// groupPoll is how often a canceled child's process group is looked at
// while it is given time to exit.
const groupPoll = 20 * time.Millisecond

// run executes one child and returns its combined output. Canceling ctx
// asks the child to stop and then stops waiting for it.
func run(ctx context.Context, in invocation) ([]byte, error) {
	var out bytes.Buffer

	err := runSplit(ctx, in, &out, &out)

	return out.Bytes(), err
}

// output executes one child and returns its stdout alone, with stderr
// folded into the error, for a child whose stdout is data.
func output(ctx context.Context, in invocation) ([]byte, error) {
	var stdout, stderr bytes.Buffer

	err := runSplit(ctx, in, &stdout, &stderr)
	if err != nil {
		if line := firstLine(stderr.String()); line != "" {
			err = fmt.Errorf("%w: %s", err, line)
		}
	}

	return stdout.Bytes(), err
}

// runSplit is the one place this package starts a process. Every program it
// runs is one the owner named: git, the go tool, a check from the tasks
// file, or the harness under test.
//
// The child leads a process group of its own, and the whole group is
// signaled: SIGTERM when ctx is canceled, SIGKILL for whatever is left of
// it after the grace period, and SIGKILL for anything the child left
// running when it exited by itself. A harness starts shells, language
// servers, and test binaries, and one that outlived its run would go on
// writing into a workspace that is about to be checked and removed.
func runSplit(ctx context.Context, in invocation, stdout, stderr *bytes.Buffer) error {
	cmd := exec.CommandContext(ctx, in.Name, in.Args...) //nolint:gosec // running named programs is this package's job
	cmd.Dir = in.Dir
	cmd.Env = in.Env
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.WaitDelay = killGrace

	var canceled time.Time

	ownGroup(cmd)

	cmd.Cancel = func() error {
		canceled = time.Now()

		return terminateGroup(cmd)
	}

	err := cmd.Run()

	if cmd.Process != nil {
		grace := time.Duration(0)
		if !canceled.IsZero() {
			grace = killGrace - time.Since(canceled)
		}

		reapGroup(cmd.Process.Pid, grace)
	}

	if err != nil {
		return fmt.Errorf("running %s: %w", in.Name, err)
	}

	return nil
}
