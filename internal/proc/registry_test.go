package proc_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/proc"
)

// sleeper starts a shell that forks a real child, which is the shape a single
// pid kill misses, and returns the group leader's pid.
func sleeper(t *testing.T) *exec.Cmd {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "sh", "-c", "sleep 60 & wait")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the sleeper: %v", err)
	}

	t.Cleanup(func() {
		_ = proc.Kill(cmd.Process.Pid) //nolint:errcheck // the test is what should have killed it
		_ = cmd.Wait()                 //nolint:errcheck // the status of a killed shell says nothing
	})

	return cmd
}

// alive excludes a zombie. A child the test has not waited on stays in the
// table after it is killed, so signaling it still succeeds and says nothing
// about whether it is running.
func alive(pid int) bool {
	//nolint:gosec,noctx // a pid the test spawned, read from a cleanup path
	out, err := exec.Command("ps", "-o", "state=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}

	state := strings.TrimSpace(string(out))

	return state != "" && !strings.HasPrefix(state, "Z")
}

// waitForGroup blocks until pgid holds at least want processes, which is
// what a single pid kill would have left behind.
func waitForGroup(t *testing.T, pgid, want int) []int {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	for {
		members := groupMembers(t, pgid)
		if len(members) >= want {
			return members
		}

		if time.Now().After(deadline) {
			t.Fatalf("group %d holds %v, want at least %d processes", pgid, members, want)
		}

		time.Sleep(10 * time.Millisecond)
	}
}

func groupMembers(t *testing.T, pgid int) []int {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "ps", "-A", "-o", "pid=,pgid=").Output()
	if err != nil {
		t.Fatalf("listing process groups: %v", err)
	}

	var pids []int

	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[1] != strconv.Itoa(pgid) {
			continue
		}

		if n, cerr := strconv.Atoi(fields[0]); cerr == nil {
			pids = append(pids, n)
		}
	}

	return pids
}

// waitGone gives the kernel a moment to reap a group. A signal is delivered
// asynchronously, so reading liveness straight after the kill is a race.
func waitGone(t *testing.T, pid int) bool {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !alive(pid) {
			return true
		}

		time.Sleep(10 * time.Millisecond)
	}

	return !alive(pid)
}

// The incident this package exists for: a daemon died mid-call and every
// child it had running stayed up, reparented to init, with no record of what
// they were.
func TestSweepKillsWhatADeadDaemonLeft(t *testing.T) {
	t.Parallel()

	reg := proc.NewRegistry(filepath.Join(t.TempDir(), "d.sock"))
	cmd := sleeper(t)

	if err := reg.Add(cmd.Process.Pid, "sleep 60"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// The shell forks, so the group comes to hold more than the pid on
	// record. Reading it straight after Start races the fork.
	members := waitForGroup(t, cmd.Process.Pid, 2)

	killed, err := reg.Sweep(t.Context())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	if len(killed) != 1 || killed[0].PID != cmd.Process.Pid {
		t.Fatalf("Sweep killed %v, want the one recorded process %d", killed, cmd.Process.Pid)
	}

	// Every member goes, including the forked child a single pid kill misses.
	for _, pid := range members {
		if !waitGone(t, pid) {
			t.Errorf("pid %d in group %d is still running after the sweep",
				pid, cmd.Process.Pid)
		}
	}

	// The record is emptied, so the same pid is not swept a second time once
	// the number belongs to something else.
	again, err := reg.Sweep(t.Context())
	if err != nil {
		t.Fatalf("second Sweep: %v", err)
	}

	if len(again) != 0 {
		t.Errorf("second Sweep killed %v, want nothing left to sweep", again)
	}
}

// Pids are reused, so a record that matched on the number alone would
// eventually kill a stranger that inherited it.
func TestSweepLeavesAReusedPIDAlone(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	reg := proc.NewRegistry(filepath.Join(dir, "d.sock"))
	cmd := sleeper(t)

	if err := reg.Add(cmd.Process.Pid, "sleep 60"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Rewrite the record with a start time no live process can have, which is
	// what a pid handed to a later process looks like.
	const staleRecord = `[{"command":"sleep 60","started":"Thu Jan  1 00:00:00 1970","pid":%d}]`

	stale := fmt.Sprintf(staleRecord, cmd.Process.Pid)
	if err := os.WriteFile(filepath.Join(dir, "spawned.json"), []byte(stale), 0o600); err != nil {
		t.Fatalf("seeding a stale record: %v", err)
	}

	killed, err := reg.Sweep(t.Context())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	if len(killed) != 0 {
		t.Fatalf("Sweep killed %v, want nothing for a pid it no longer owns", killed)
	}

	if !alive(cmd.Process.Pid) {
		t.Errorf("pid %d was killed on a start time that did not match", cmd.Process.Pid)
	}
}

// Removing what was waited on is what keeps the record to processes that are
// actually still running.
func TestRemoveDropsAFinishedProcess(t *testing.T) {
	t.Parallel()

	reg := proc.NewRegistry(filepath.Join(t.TempDir(), "d.sock"))
	cmd := sleeper(t)

	if err := reg.Add(cmd.Process.Pid, "sleep 60"); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := reg.Remove(cmd.Process.Pid); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	killed, err := reg.Sweep(t.Context())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	if len(killed) != 0 {
		t.Errorf("Sweep killed %v, want nothing after the process was removed", killed)
	}
}

func TestKillRefusesAPIDThatNamesEveryProcess(t *testing.T) {
	t.Parallel()

	for _, pid := range []int{0, -1} {
		if err := proc.Kill(pid); !errors.Is(err, proc.ErrBadPID) {
			t.Errorf("Kill(%d) = %v, want ErrBadPID", pid, err)
		}
	}
}
