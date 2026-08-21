package app_test

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/app"
	"github.com/kyleking/wavez/internal/config"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/llm/fake"
	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/router"
	"github.com/kyleking/wavez/internal/vcs"
)

func requireJJ(t *testing.T) {
	t.Helper()

	if _, err := exec.LookPath("jj"); err != nil {
		t.Skip("jj binary not found on PATH, skipping")
	}
}

func newJJRepo(t *testing.T) string {
	t.Helper()
	requireJJ(t)

	root := t.TempDir()

	cmd := exec.CommandContext(context.Background(), "jj", "git", "init", "--colocate")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("jj git init --colocate: %v: %s", err, out)
	}

	return root
}

// TestNew_LoopRunReportsActionableErrorWhenNotAJJRepo proves the wiring
// through app.New surfaces vcs's actionable message rather than failing
// obscurely or silently skipping checkpointing, per DESIGN.md's VCS
// decision: a checkpoint that cannot be taken is not a checkpoint that
// succeeded.
func TestNew_LoopRunReportsActionableErrorWhenNotAJJRepo(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := config.Defaults(root)
	provider := fake.New("balanced", fake.Turn{Text: []string{"ok"}})

	a, err := app.New(context.Background(), root, cfg, permission.AllowAll(),
		app.WithProviders(tierProviders(provider)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if cerr := a.Close(); cerr != nil {
			t.Errorf("Close: %v", cerr)
		}
	})

	th, err := a.OpenThread("t1", []string{root})
	if err != nil {
		t.Fatalf("OpenThread: %v", err)
	}

	prefix := agent.Prefix{System: a.SystemPrefix}

	_, err = a.Loop.Run(context.Background(), th, prefix, "do something", router.Input{})
	if err == nil {
		t.Fatal("Run: want an error, root is not a jj repository")
	}
	if !errors.Is(err, vcs.ErrNotJJRepo) {
		t.Fatalf("Run error = %v, want it to wrap vcs.ErrNotJJRepo", err)
	}
	if !strings.Contains(err.Error(), vcs.InitHint) {
		t.Fatalf("Run error = %q, want it to name the actionable fix %q", err.Error(), vcs.InitHint)
	}
}

// TestNew_LoopRunSucceedsWithCheckpointInAColocatedJJRepo proves the same
// wiring captures a real checkpoint end to end once root is the colocated
// jj repo DESIGN.md's VCS decision assumes.
func TestNew_LoopRunSucceedsWithCheckpointInAColocatedJJRepo(t *testing.T) {
	t.Parallel()

	root := newJJRepo(t)
	cfg := config.Defaults(root)
	// Verify runs the real gate.Gates against this repo, which has no
	// go.mod, so it fails and each of these extra scripted turns absorbs
	// one round of the resulting feedback loop; only the checkpoint that
	// Run captures once at the start is under test here.
	provider := fake.New("balanced",
		fake.Turn{Text: []string{"ok"}, StopReason: llm.StopEndTurn},
		fake.Turn{Text: []string{"ok"}, StopReason: llm.StopEndTurn},
		fake.Turn{Text: []string{"ok"}, StopReason: llm.StopEndTurn},
	)

	a, err := app.New(context.Background(), root, cfg, permission.AllowAll(),
		app.WithProviders(tierProviders(provider)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if cerr := a.Close(); cerr != nil {
			t.Errorf("Close: %v", cerr)
		}
	})

	th, err := a.OpenThread("t1", []string{root})
	if err != nil {
		t.Fatalf("OpenThread: %v", err)
	}

	prefix := agent.Prefix{System: a.SystemPrefix}

	out, err := a.Loop.Run(context.Background(), th, prefix, "do something", router.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Checkpoint == "" {
		t.Fatal("Outcome.Checkpoint is empty in a colocated jj repo")
	}
}
