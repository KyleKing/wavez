package bench

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"syscall"
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

// killGrace is how long a canceled child has to exit after SIGTERM before
// it is killed and its pipes are abandoned.
const killGrace = 5 * time.Second

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
func runSplit(ctx context.Context, in invocation, stdout, stderr *bytes.Buffer) error {
	cmd := exec.CommandContext(ctx, in.Name, in.Args...) //nolint:gosec // running named programs is this package's job
	cmd.Dir = in.Dir
	cmd.Env = in.Env
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = killGrace

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("running %s: %w", in.Name, err)
	}

	return nil
}
