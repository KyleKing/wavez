//go:build unix

package bench

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

// ownGroup makes the child the leader of a new process group, so that it
// and everything it starts can be signaled together.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminateGroup asks the child's whole process group to stop.
func terminateGroup(cmd *exec.Cmd) error {
	return signalGroup(cmd.Process.Pid, syscall.SIGTERM)
}

// signalGroup signals the process group led by pid. A group with nobody
// left in it is not an error.
func signalGroup(pid int, sig syscall.Signal) error {
	if err := syscall.Kill(-pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err //nolint:wrapcheck // exec.Cmd reports what Cancel returns as it is
	}

	return nil
}

// groupAlive reports whether any process is left in the group led by pid.
// The id of a group is not handed to a new process while the group has
// members, so a group that reads as alive is still the child's.
func groupAlive(pid int) bool {
	return !errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH)
}

// reapGroup waits up to grace for the group led by pid to empty and kills
// what is left. A group that was never asked to stop gets no grace.
func reapGroup(pid int, grace time.Duration) {
	for deadline := time.Now().Add(grace); groupAlive(pid); time.Sleep(groupPoll) {
		if !time.Now().Before(deadline) {
			// The group is going away either way, and a kill that fails
			// leaves nothing further to try.
			_ = signalGroup(pid, syscall.SIGKILL) //nolint:errcheck // see above

			break
		}
	}

	// A kill is delivered, not finished, when the call returns: a process
	// inside a system call completes it first, and a rename it was making
	// still lands. The caller is about to read and remove the workspace, so
	// it waits here until the group is empty, for as long as a kill that
	// works can take.
	for deadline := time.Now().Add(killSettle); groupAlive(pid) && time.Now().Before(deadline); {
		time.Sleep(groupPoll)
	}
}
