package config_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/config"
	"github.com/kyleking/wavez/internal/routine"
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
	if cfg.Tiers != want.Tiers ||
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
fast = new { model = "custom-fast" }
debounceMs = 750
localPort = 8123
localStartTimeoutSeconds = 120
balanced = new {
  model = "custom-balanced"
  baseURL = "https://m4.example.ts.net/v1"
  keyCommand = "security find-generic-password -w -s wavez-balanced"
}
preToolUseHook {
  ".wavez/hooks/pre.sh"
  "--strict"
}
hookTimeoutMs = 250
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

	for name, tc := range map[string]struct{ got, want string }{
		"FastModel":          {cfg.Tiers.Fast.Model, "custom-fast"},
		"BalancedModel":      {cfg.Tiers.Balanced.Model, "custom-balanced"},
		"DeepModel":          {cfg.Tiers.Deep.Model, config.DefaultDeepModel},
		"BalancedBaseURL":    {cfg.Tiers.Balanced.BaseURL, "https://m4.example.ts.net/v1"},
		"BalancedKeyCommand": {cfg.Tiers.Balanced.KeyCommand, "security find-generic-password -w -s wavez-balanced"},
		"PreToolUseHook":     {strings.Join(cfg.PreToolUseHook, " "), ".wavez/hooks/pre.sh --strict"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", name, tc.got, tc.want)
		}
	}
	if cfg.GateDebounce != 750*time.Millisecond {
		t.Errorf("GateDebounce = %v, want 750ms", cfg.GateDebounce)
	}
	if cfg.LocalPort != 8123 {
		t.Errorf("LocalPort = %d, want 8123", cfg.LocalPort)
	}
	if cfg.LocalStartTimeout != 120*time.Second {
		t.Errorf("LocalStartTimeout = %v, want 2m", cfg.LocalStartTimeout)
	}
	if len(cfg.Context) != 1 || cfg.Context[0] != "AGENTS.md" {
		t.Errorf("Context = %v, want [AGENTS.md]", cfg.Context)
	}
	if len(cfg.PostToolUseHook) != 0 {
		t.Errorf("PostToolUseHook = %v, want empty when unset", cfg.PostToolUseHook)
	}
	if cfg.HookTimeout != 250*time.Millisecond {
		t.Errorf("HookTimeout = %v, want 250ms", cfg.HookTimeout)
	}
}

func TestLoad_LinksParsePatternsInOrder(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, config.FileName), `
amends ".wavez/Wavez.pkl"

links {
  new LinkPattern { pattern = "#(\\d+)"; url = "https://github.com/kyleking/wavez/pull/$1" }
  new LinkPattern { pattern = "\\b(ENG-\\d+)\\b"; url = "https://linear.app/team/issue/$1" }
}
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

	cfg, _, err := loader.Load(context.Background(), root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := []config.LinkPattern{
		{Pattern: `#(\d+)`, URL: "https://github.com/kyleking/wavez/pull/$1"},
		{Pattern: `\b(ENG-\d+)\b`, URL: "https://linear.app/team/issue/$1"},
	}
	if len(cfg.Links) != len(want) {
		t.Fatalf("Links = %+v, want %+v", cfg.Links, want)
	}
	for i, w := range want {
		if cfg.Links[i] != w {
			t.Errorf("Links[%d] = %+v, want %+v", i, cfg.Links[i], w)
		}
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

func TestLoad_RoutinesCompileAgainstTheActionRegistry(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, config.FileName), `
amends ".wavez/Wavez.pkl"

routines {
  ["nightly"] {
    triggers { "schedule" }
    paths { "*.go" }
    intervalSeconds = 3600
    concurrencyKey = "heavy"
    concurrency = "cancel-in-progress"
    steps {
      new {
        name = "vet"
        action = "run"
        params { ["argv"] = new Listing { "go"; "vet"; "./..." } }
      }
      new {
        name = "test"
        action = "run"
        parents { "vet" }
        params { ["argv"] = new Listing { "go"; "test"; "./..." } }
      }
    }
  }
}
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

	cfg, _, err := loader.Load(context.Background(), root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	def, ok := cfg.Routines["nightly"]
	if !ok {
		t.Fatalf("Routines = %v, want a nightly routine", cfg.Routines)
	}
	if def.Interval != time.Hour || def.ConcurrencyKey != "heavy" ||
		def.Concurrency != routine.CancelInProgress || !def.Enabled {
		t.Errorf("nightly = %+v, want the file's schedule and concurrency", def)
	}
	if len(def.Steps) != 2 || len(def.Steps[1].Parents) != 1 {
		t.Fatalf("steps = %+v, want a two-step DAG with one parent edge", def.Steps)
	}

	assertCompiles(t, root, cfg.Routines)
}

// assertCompiles checks that a loaded definition survives the step every
// project pays on startup: compiling against a real action registry.
func assertCompiles(t *testing.T, root string, defs map[string]routine.Definition) {
	t.Helper()

	set, err := routine.CompileSet(defs, routine.NewRegistry(routine.RunAction(root)), "hash")
	if err != nil {
		t.Fatalf("CompileSet: %v", err)
	}

	compiled, ok := set.Get("nightly")
	if !ok {
		t.Fatalf("compiled set has no nightly routine")
	}
	if len(compiled.Order) != 2 {
		t.Errorf("Order = %v, want one wave per dependency level", compiled.Order)
	}
	if !compiled.MatchesPath("internal/lease.go") || compiled.MatchesPath("README.md") {
		t.Errorf("MatchesPath does not honor the routine's path globs")
	}
}

// Seeing is a capability rather than a difficulty, so the tier that looks at
// an image sits outside the three a routing decision picks between. A project
// that names none cannot look at anything, which is what a tool asks before
// it produces an image rather than after the request is refused.
func TestLoad_VisionTierIsSeparateFromTheRoutedThree(t *testing.T) {
	t.Parallel()

	loader, err := config.NewLoader(context.Background())
	if err != nil {
		t.Fatalf("NewLoader: %v", err)
	}
	t.Cleanup(func() {
		if err := loader.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	bare := t.TempDir()
	if cfg, _, err := loader.Load(context.Background(), bare); err != nil {
		t.Fatalf("Load: %v", err)
	} else if cfg.Vision != nil {
		t.Errorf("Vision = %+v with no config, want nil", cfg.Vision)
	}

	root := t.TempDir()
	writeFile(t, filepath.Join(root, config.FileName), `
amends ".wavez/Wavez.pkl"

vision = new Tier {
  model = "glm-4.6v"
  baseURL = "https://api.z.ai/api/coding/paas/v4"
  keyCommand = "security find-generic-password -w -s wavez-zai"
  thinking = false
}
`)

	cfg, _, err := loader.Load(context.Background(), root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Vision == nil {
		t.Fatal("Vision = nil, want the configured tier")
	}

	if cfg.Vision.Model != "glm-4.6v" || cfg.Vision.KeyCommand == "" {
		t.Errorf("Vision = %+v, want the model and key command it named", cfg.Vision)
	}

	if cfg.Tiers.Balanced.Model == "glm-4.6v" {
		t.Error("the vision tier replaced balanced, want it separate from the routed three")
	}
}
