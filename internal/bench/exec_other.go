//go:build !unix

package bench

import (
	"os/exec"
	"time"
)

// ownGroup does nothing where there are no process groups to signal.
func ownGroup(*exec.Cmd) {}

// terminateGroup stops the child alone, the most this platform offers.
func terminateGroup(cmd *exec.Cmd) error {
	return cmd.Process.Kill() //nolint:wrapcheck // exec.Cmd reports what Cancel returns as it is
}

// reapGroup has no group to wait for.
func reapGroup(int, time.Duration) {}
