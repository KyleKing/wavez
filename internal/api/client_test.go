package api_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/daemon"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/llm/fake"
	"github.com/kyleking/wavez/internal/tool"
)

func serve(t *testing.T) string {
	t.Helper()

	// Not t.TempDir(): its path plus the test name overruns the 104-byte
	// sun_path limit and connect fails with a bare "invalid argument".
	//nolint:usetesting // t.TempDir()'s path overruns the 104-byte sun_path limit
	dir, err := os.MkdirTemp("", "wz")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() {
		if rerr := os.RemoveAll(dir); rerr != nil {
			t.Errorf("cleanup: %v", rerr)
		}
	})
	sock := filepath.Join(dir, "d.sock")
	broker := daemon.NewBroker()
	local := fake.New("local", fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn})
	hosted := fake.New("hosted", fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn})
	loop := agent.New(local, hosted, tool.NewRegistry(), broker.Gate())

	srv, derr := daemon.New(sock,
		daemon.WithLoop(loop), daemon.WithBroker(broker), daemon.WithLogDir(t.TempDir()))
	if derr != nil {
		t.Fatalf("daemon.New: %v", derr)
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	serveErr := make(chan error, 1)
	go func() {
		defer close(done)
		serveErr <- srv.Serve(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		if serr := <-serveErr; serr != nil {
			t.Errorf("Serve: %v", serr)
		}
	})

	return sock
}

// dialWhenUp retries until the daemon has bound its socket, since Serve binds
// asynchronously and the deadline belongs to ctx rather than to a fixed sleep.
func dialWhenUp(ctx context.Context, t *testing.T, sock string, opts ...api.DialOption) (*api.Client, error) {
	t.Helper()

	for {
		c, err := api.Dial(ctx, sock, opts...)
		if err == nil {
			return c, nil
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("dialing %s: %w", sock, err)
		}
	}
}

func TestClientHandshakeAndThreadRoundTrip(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	c, err := dialWhenUp(ctx, t, serve(t))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() {
		if cerr := c.Close(); cerr != nil {
			t.Errorf("close: %v", cerr)
		}
	})

	created, err := c.Do(ctx, api.Command{Kind: api.CmdNew, Prompt: "hello", Dirs: []string{"."}})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	if created.Thread == nil || created.Thread.ID == "" {
		t.Fatalf("new returned no thread: %+v", created)
	}

	listed, err := c.Do(ctx, api.Command{Kind: api.CmdList})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed.Threads) != 1 {
		t.Fatalf("list returned %d threads, want 1", len(listed.Threads))
	}
	if listed.Threads[0].ID != created.Thread.ID {
		t.Fatalf("list returned %q, want %q", listed.Threads[0].ID, created.Thread.ID)
	}
}

// serveFleet builds a daemon that loads a project lazily per root, so a
// single socket serves several projects the way a per-laptop daemon does.
func serveFleet(t *testing.T) string {
	t.Helper()

	//nolint:usetesting // t.TempDir()'s path overruns the 104-byte sun_path limit
	dir, err := os.MkdirTemp("", "wz")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() {
		if rerr := os.RemoveAll(dir); rerr != nil {
			t.Errorf("cleanup: %v", rerr)
		}
	})
	sock := filepath.Join(dir, "d.sock")
	broker := daemon.NewBroker()
	local := fake.New("local", fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn})
	hosted := fake.New("hosted", fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn})
	loop := agent.New(local, hosted, tool.NewRegistry(), broker.Gate())

	loader := func(_ context.Context, root string) (*daemon.Project, error) {
		return daemon.NewProject(root, daemon.ProjectConfig{Loop: loop, LogDir: filepath.Join(root, "threads")})
	}

	srv, derr := daemon.New(sock, daemon.WithLoader(loader), daemon.WithBroker(broker))
	if derr != nil {
		t.Fatalf("daemon.New: %v", derr)
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	serveErr := make(chan error, 1)
	go func() {
		defer close(done)
		serveErr <- srv.Serve(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		if serr := <-serveErr; serr != nil {
			t.Errorf("Serve: %v", serr)
		}
	})

	return sock
}

// TestClientAllRoots_OverridesTheDefaultRoot covers WithDefaultRoot's other
// half: a client dialed with a default root still reaches the whole fleet
// when a command opts out with AllRoots, which is what lets Home's fleet
// scope share one Client with its scoped list requests.
func TestClientAllRoots_OverridesTheDefaultRoot(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	sock := serveFleet(t)
	rootA, rootB := t.TempDir(), t.TempDir()

	c, err := dialWhenUp(ctx, t, sock, api.WithDefaultRoot(rootA))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() {
		if cerr := c.Close(); cerr != nil {
			t.Errorf("close: %v", cerr)
		}
	})

	if _, err := c.Do(ctx, api.Command{Kind: api.CmdNew, Root: rootA, Prompt: "a", Dirs: []string{rootA}}); err != nil {
		t.Fatalf("new in root A: %v", err)
	}
	if _, err := c.Do(ctx, api.Command{Kind: api.CmdNew, Root: rootB, Prompt: "b", Dirs: []string{rootB}}); err != nil {
		t.Fatalf("new in root B: %v", err)
	}

	scoped, err := c.Do(ctx, api.Command{Kind: api.CmdList})
	if err != nil {
		t.Fatalf("scoped list: %v", err)
	}
	if len(scoped.Threads) != 1 {
		t.Fatalf("scoped list (default root stamped) = %d threads, want 1", len(scoped.Threads))
	}

	fleet, err := c.Do(ctx, api.Command{Kind: api.CmdList, AllRoots: true})
	if err != nil {
		t.Fatalf("fleet list: %v", err)
	}
	if len(fleet.Threads) != 2 {
		t.Fatalf("fleet list (AllRoots) = %d threads, want 2", len(fleet.Threads))
	}
}

func TestDialRefusesAMissingSocket(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	if _, err := api.Dial(ctx, filepath.Join(t.TempDir(), "absent.sock")); err == nil {
		t.Fatal("dialing a missing socket succeeded")
	}
}
