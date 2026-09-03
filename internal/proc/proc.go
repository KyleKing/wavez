// Package proc keeps the processes wavez spawns from outliving the daemon
// that started them.
//
// Two things go wrong without it. Killing a child by its pid misses whatever
// that child forked, so a command run through a shell operator keeps running.
// And a daemon that is force-killed mid-call leaves every child it had
// running with no record anywhere of what they were: one incident produced 19
// orphaned processes pinned at 40-49% CPU each, the oldest about 10 hours
// old, because a Textual app whose terminal hangs up spins instead of
// exiting.
package proc

import (
	"errors"
	"fmt"
	"syscall"
)

// ErrBadPID is a pid no signal may be sent to. Zero and negative pids name
// process groups and every process the caller may signal, so a zero read out
// of an empty record must not reach the kernel.
var ErrBadPID = errors.New("proc: not a process id")

// Kill ends pid and every process sharing its group.
//
// The group is what matters. A command run as `sh -c` exec-replaces itself
// only when it is one simple command, so anything with an operator forks a
// real child that a single-pid kill leaves behind. Callers put the child in
// its own group, either with Setpgid or with the Setsid a pty child already
// gets, which is what keeps the group holding that command and nothing else.
func Kill(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("%w: %d", ErrBadPID, pid)
	}

	if err := syscall.Kill(-pid, syscall.SIGKILL); err == nil {
		return nil
	}

	// A child that never led a group has no group to signal, which fails the
	// same way an already-exited group does. Both are answered by signaling
	// the process on its own.
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("killing %d: %w", pid, err)
	}

	return nil
}
