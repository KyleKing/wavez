package app_test

import (
	"context"
	"os"
	"path/filepath"
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

	local := fake.New("local", fake.Turn{Text: []string{"ok"}})
	hosted := fake.New("hosted", fake.Turn{Text: []string{"ok"}})

	a, err := app.New(context.Background(), root, cfg, permission.AllowAll(),
		app.WithProviders(local, hosted))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if a.SystemPrefix != "The store owns SQLite. Gates trigger on change events." {
		t.Errorf("SystemPrefix = %q, want the Architecture section only", a.SystemPrefix)
	}
	if got, want := len(a.Tools.Names()), 6; got != want {
		t.Errorf("len(Tools.Names()) = %d, want %d: %v", got, want, a.Tools.Names())
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

	local := fake.New("local")
	hosted := fake.New("hosted")

	a, err := app.New(context.Background(), root, cfg, permission.AllowAll(),
		app.WithProviders(local, hosted))
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
