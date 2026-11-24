package warm_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/warm"
)

// holderEnv makes the test binary act as a warm run: it holds the lock for
// the session named in the variable until it is told to end.
const holderEnv = "WAVEZ_TEST_WARM_HOLDER"

func TestMain(m *testing.M) {
	if spec := os.Getenv(holderEnv); spec != "" {
		root, id, _ := strings.Cut(spec, "\n")

		release, err := warm.Hold(context.Background(), root, id)
		if err != nil {
			os.Exit(3)
		}

		// The marker says the lock is held, so the test need not guess.
		//nolint:gosec // a directory the test made
		if err := os.WriteFile(filepath.Join(root, "held-"+strconv.Itoa(os.Getpid())), nil, 0o600); err != nil {
			os.Exit(4)
		}

		time.Sleep(time.Minute)
		release()
		os.Exit(0)
	}

	os.Exit(m.Run())
}

// holder starts a process that holds the session's warm lock and waits
// until it does.
func holder(t *testing.T, root, id string) *exec.Cmd {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), os.Args[0]) //nolint:gosec // this test binary
	cmd.Env = append(os.Environ(), holderEnv+"="+root+"\n"+id)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		// Both fail when a test already ended the holder, which is fine.
		_ = cmd.Process.Kill() //nolint:errcheck // see above
		_ = cmd.Wait()         //nolint:errcheck // see above
	})

	marker := filepath.Join(root, "held-"+strconv.Itoa(cmd.Process.Pid))
	require.Eventually(t, func() bool {
		_, err := os.Stat(marker)

		return err == nil
	}, 10*time.Second, 10*time.Millisecond, "the holder never took the lock")

	return cmd
}

func TestWait_ReturnsAtOnceWithNoRun(t *testing.T) {
	t.Parallel()

	start := time.Now()
	warm.Wait(t.Context(), t.TempDir(), "s1")
	assert.Less(t, time.Since(start), time.Second)
}

func TestWait_WaitsForTheRunInFlight(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	run := holder(t, root, "s1")

	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()

	start := time.Now()
	warm.Wait(ctx, root, "s1")
	assert.GreaterOrEqual(t, time.Since(start), 250*time.Millisecond, "a live run is waited for until ctx ends")

	require.NoError(t, run.Process.Kill())

	start = time.Now()
	warm.Wait(t.Context(), root, "s1")
	assert.Less(t, time.Since(start), 5*time.Second, "a run that died released the lock by dying")
}

func TestHold_EndsTheRunBeforeIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	first := holder(t, root, "s1")

	release, err := warm.Hold(t.Context(), root, "s1")
	require.NoError(t, err)

	defer release()

	// SIGTERM ends the first holder, whose exit is what freed the lock.
	done := make(chan error, 1)
	go func() { done <- first.Wait() }()

	select {
	case err := <-done:
		require.Error(t, err, "the run before was told to end")
	case <-time.After(5 * time.Second):
		t.Fatal("the run before is still alive")
	}

	// Another session's lock is its own.
	other, err := warm.Hold(t.Context(), root, "s2")
	require.NoError(t, err)
	other()
}

func TestHold_RefusesAnIDThatCannotNameAFile(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"", "../x", "a/b"} {
		_, err := warm.Hold(t.Context(), t.TempDir(), id)
		require.Error(t, err, id)
		require.Error(t, warm.Start("/bin/true", t.TempDir(), id), id)
	}
}
