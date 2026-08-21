package app_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/app"
	"github.com/kyleking/wavez/internal/config"
	"github.com/kyleking/wavez/internal/llm/fake"
	"github.com/kyleking/wavez/internal/permission"
)

func TestNew_ConstructsAndClosesTwiceWithoutError(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "AGENTS.md"), agentsMD)

	cfg := config.Defaults(root)
	cfg.Context = []string{"AGENTS.md#Architecture"}

	provider := fake.New("balanced", fake.Turn{Text: []string{"ok"}})

	a, err := app.New(context.Background(), root, cfg, permission.AllowAll(),
		app.WithProviders(tierProviders(provider)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if !strings.HasPrefix(a.SystemPrefix, app.BaseSystem) {
		t.Errorf("SystemPrefix does not open with the base instructions: %q", a.SystemPrefix)
	}
	if !strings.Contains(a.SystemPrefix, "The store owns SQLite. Gates trigger on change events.") {
		t.Errorf("SystemPrefix is missing the listed Architecture section: %q", a.SystemPrefix)
	}
	if got, want := len(a.Tools.Names()), 10; got != want {
		t.Errorf("len(Tools.Names()) = %d, want %d: %v", got, want, a.Tools.Names())
	}

	// A plan thread must be unable to reach an editing tool, not merely be
	// told not to: the registry refuses what it dropped, so a model naming
	// an unadvertised tool gets ErrNotFound rather than an edit.
	for _, name := range []string{"str_replace", "write", "shell", "rename", "delete"} {
		if _, err := a.PlanTools.Get(name); err == nil {
			t.Errorf("PlanTools.Get(%q) succeeded; a plan thread could edit", name)
		}
	}

	for _, name := range app.ReadOnlyTools {
		if _, err := a.PlanTools.Get(name); err != nil {
			t.Errorf("PlanTools.Get(%q) = %v, want the tool", name, err)
		}
	}
	if _, err := os.Stat(a.SandboxDir); err != nil {
		t.Errorf("SandboxDir %s does not exist: %v", a.SandboxDir, err)
	}

	if err := a.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if _, err := os.Stat(a.SandboxDir); !os.IsNotExist(err) {
		t.Errorf("SandboxDir %s still exists after Close", a.SandboxDir)
	}

	if err := a.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestNew_OpensAndClosesTrackedThreads(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := config.Defaults(root)

	provider := fake.New("balanced")

	a, err := app.New(context.Background(), root, cfg, permission.AllowAll(),
		app.WithProviders(tierProviders(provider)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if err := a.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	th, err := a.OpenThread("t1", []string{root})
	if err != nil {
		t.Fatalf("OpenThread: %v", err)
	}
	if err := th.AppendUser(context.Background(), "hi"); err != nil {
		t.Fatalf("AppendUser: %v", err)
	}

	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
