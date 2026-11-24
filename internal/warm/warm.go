// Package warm runs a session's checks ahead of being asked for them, in a
// process of its own that nothing waits on. The model spends most of a turn
// generating its next step, and the machine is idle while it does. A check
// started when a file is written has usually finished by the time the agent
// asks for it or tries to stop, and what it left behind (the compiler's
// cache, the test results go keeps, the passes wavez keeps, and the verdict
// in the session) makes that later run a matter of reading answers back.
//
// One run per session is alive at a time. A newer one ends the one before
// it, whose tree has changed under it, and a caller that needs the answer
// can wait for the run in flight to end.
package warm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	filePerm = 0o600
	dirPerm  = 0o750
	// The poll interval is how often a waiter looks at the lock again.
	poll = 50 * time.Millisecond
	// The yield time is how long a run that was asked to end is given before the
	// next one starts beside it.
	yield = 2 * time.Second
	// The niceness is added to a warm run's own, so that a command the agent
	// runs meanwhile gets the processor first.
	niceness = "10"
	// The pidLength bound is more than a process id and a newline take.
	pidLength = 32
)

// errSession is a session id that cannot name a file.
var errSession = errors.New("session id cannot name a file")

// lockPath is the file a session's warm run holds a lock on, and writes its
// process id into, for as long as it runs.
func lockPath(root, id string) (string, error) {
	if id == "" || !filepath.IsLocal(id) || strings.ContainsAny(id, `/\`) {
		return "", fmt.Errorf("%w: %q", errSession, id)
	}

	return filepath.Join(root, ".wavez", "sessions", id+".warm"), nil
}

// Start runs `bin warm` for the session in a new session of its own, so it
// outlives the hook that started it, and returns without waiting. Its
// output goes nowhere: what it finds is read back from the caches.
func Start(bin, root, id string, args ...string) error {
	if _, err := lockPath(root, id); err != nil {
		return err
	}

	argv := append([]string{bin, "warm", "--root", root, "--session", id}, args...)

	// The priority is lowered by nice and not from inside the run: a
	// process can only lower its own thread's, and the checks are children
	// of whichever thread starts them. Without nice the run keeps its own.
	if nice, err := exec.LookPath("nice"); err == nil {
		argv = append([]string{nice, "-n", niceness}, argv...)
	}

	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec,noctx // this binary, and it must outlive the caller
	cmd.Dir = root
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting the warm run: %w", err)
	}

	// The child is on its own, and its exit status is nobody's to collect.
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("releasing the warm run: %w", err)
	}

	return nil
}

// Hold makes the calling process the session's one warm run: it asks the
// run before it to end and takes the lock. The
// lock is the kernel's, held for the open file, so a run that is killed
// releases it by dying. The returned function releases it.
func Hold(ctx context.Context, root, id string) (func(), error) {
	path, err := lockPath(root, id)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return nil, fmt.Errorf("creating the session directory: %w", err)
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, filePerm) //nolint:gosec // id is validated
	if err != nil {
		return nil, fmt.Errorf("opening the warm lock: %w", err)
	}

	if !locked(file) {
		end(file)

		waitCtx, cancel := context.WithTimeout(ctx, yield)
		err := waitFor(waitCtx, file)

		cancel()

		if err != nil {
			return nil, errors.Join(fmt.Errorf("the warm run before this one did not end: %w", err), file.Close())
		}
	}

	if err := record(file); err != nil {
		return nil, errors.Join(err, file.Close())
	}

	return func() { _ = file.Close() }, nil //nolint:errcheck // closing is what releases the lock
}

// Wait returns when no warm run of the session is alive, or when ctx ends.
func Wait(ctx context.Context, root, id string) {
	path, err := lockPath(root, id)
	if err != nil {
		return
	}

	file, err := os.OpenFile(path, os.O_RDWR, filePerm) //nolint:gosec // id is validated
	if err != nil {
		return
	}

	defer file.Close() //nolint:errcheck // closing is what releases the lock

	// An error is ctx ending, and the caller goes on without the answer.
	_ = waitFor(ctx, file) //nolint:errcheck // see above
}

// locked takes the lock if nobody holds it and reports whether it did.
func locked(file *os.File) bool {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
}

// waitFor takes the lock, waiting for whoever holds it.
func waitFor(ctx context.Context, file *os.File) error {
	for !locked(file) {
		select {
		case <-ctx.Done():
			return ctx.Err() //nolint:wrapcheck // the caller names what it was waiting for
		case <-time.After(poll):
		}
	}

	return nil
}

// end asks the run whose process id the file holds to stop. It leads a
// session of its own, and ends its checks when it is told to.
func end(file *os.File) {
	body := make([]byte, pidLength)

	n, err := file.ReadAt(body, 0)
	if n == 0 && err != nil {
		return
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(body[:n])))
	if err != nil || pid <= 1 || pid == os.Getpid() {
		return
	}

	// A process that is already gone has nothing left to end.
	_ = syscall.Kill(pid, syscall.SIGTERM) //nolint:errcheck // see above
}

// record writes this process's id into the lock file.
func record(file *os.File) error {
	if err := file.Truncate(0); err != nil {
		return fmt.Errorf("clearing the warm lock: %w", err)
	}

	if _, err := file.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0); err != nil {
		return fmt.Errorf("writing the warm lock: %w", err)
	}

	return nil
}
