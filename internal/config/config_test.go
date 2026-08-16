package config_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/config"
)

func TestLoad_DefaultsWithNoConfigFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	loader, err := config.NewLoader(context.Background())
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	t.Cleanup(func() {
		if err := loader.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	cfg, inference, err := loader.Load(context.Background(), root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if inference != nil {
		t.Errorf("inference = %+v, want nil for an empty project", inference)
	}

	want := config.Defaults(root)
	if cfg.LocalModel != want.LocalModel || cfg.HostedModel != want.HostedModel ||
		cfg.ContextWindow != want.ContextWindow || cfg.GateDebounce != want.GateDebounce ||
		cfg.FullRunCadence != want.FullRunCadence {
		t.Errorf("Load() = %+v, want defaults %+v", cfg, want)
	}
	if len(cfg.Context) != 0 {
		t.Errorf("Context = %v, want empty with no config file", cfg.Context)
	}
}

func TestLoad_AmendedConfigOverridesFields(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "AGENTS.md"), "# AGENTS\ninstructions")

	writeFile(t, filepath.Join(root, config.FileName), `
amends ".wavez/Wavez.pkl"

context {
  "AGENTS.md"
}
localModel = "custom-local"
debounceMs = 750
`)

	loader, err := config.NewLoader(context.Background())
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	t.Cleanup(func() {
		if err := loader.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	cfg, inference, err := loader.Load(context.Background(), root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if inference != nil {
		t.Errorf("inference = %+v, want nil when a config file exists", inference)
	}

	if cfg.LocalModel != "custom-local" {
		t.Errorf("LocalModel = %q, want %q", cfg.LocalModel, "custom-local")
	}
	if cfg.HostedModel != config.DefaultHostedModel {
		t.Errorf("HostedModel = %q, want unchanged default %q", cfg.HostedModel, config.DefaultHostedModel)
	}
	if cfg.GateDebounce != 750*time.Millisecond {
		t.Errorf("GateDebounce = %v, want 750ms", cfg.GateDebounce)
	}
	if len(cfg.Context) != 1 || cfg.Context[0] != "AGENTS.md" {
		t.Errorf("Context = %v, want [AGENTS.md]", cfg.Context)
	}
}

func TestLoad_WithExtraAppendsToContext(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	loader, err := config.NewLoader(context.Background())
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	t.Cleanup(func() {
		if err := loader.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	cfg, _, err := loader.Load(context.Background(), root, config.WithExtra("NOTES.md"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Context) != 1 || cfg.Context[0] != "NOTES.md" {
		t.Errorf("Context = %v, want [NOTES.md]", cfg.Context)
	}
}

func TestDiscover(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantCmds map[string]string
		name     string
		file     string
		content  string
		wantOK   bool
	}{
		{
			name:     "package.json",
			file:     "package.json",
			content:  `{"scripts": {"test": "vitest", "lint": "eslint ."}}`,
			wantOK:   true,
			wantCmds: map[string]string{"test": "npm run test", "lint": "npm run lint"},
		},
		{
			name:     "Makefile",
			file:     "Makefile",
			content:  "test:\n\tgo test ./...\nfmt:\n\tgofmt -l .\n",
			wantOK:   true,
			wantCmds: map[string]string{"test": "make test", "format": "make fmt"},
		},
		{
			name:     "pyproject.toml",
			file:     "pyproject.toml",
			content:  "[project]\nname = \"x\"\n[tool.ruff]\nline-length = 100\n",
			wantOK:   true,
			wantCmds: map[string]string{"test": "pytest", "lint": "ruff check .", "format": "ruff format ."},
		},
		{
			name:     "mise.toml",
			file:     "mise.toml",
			content:  "[tasks.test]\nrun = \"go test ./...\"\n[tasks.lint]\nrun = \"golangci-lint run\"\n",
			wantOK:   true,
			wantCmds: map[string]string{"test": "mise run test", "lint": "mise run lint"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			writeFile(t, filepath.Join(root, tt.file), tt.content)

			inf, ok := config.Discover(root)
			if ok != tt.wantOK {
				t.Fatalf("Discover() ok = %v, want %v", ok, tt.wantOK)
			}
			if inf.Source != tt.file {
				t.Errorf("Source = %q, want %q", inf.Source, tt.file)
			}
			for gate, want := range tt.wantCmds {
				if got := inf.Commands[gate]; got != want {
					t.Errorf("Commands[%q] = %q, want %q", gate, got, want)
				}
			}
		})
	}
}

func TestDiscover_NoRecognizedFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	if _, ok := config.Discover(root); ok {
		t.Error("Discover() ok = true, want false for an empty project")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}
