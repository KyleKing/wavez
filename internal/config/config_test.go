package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/config"
)

func write(t *testing.T, body string) string {
	t.Helper()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, config.FileName), []byte(body), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}

	return root
}

func TestLoad_MissingFileYieldsDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.MaxRounds != config.DefaultMaxRounds || len(cfg.Checks) != 0 {
		t.Errorf("got %+v, want only the default max rounds", cfg)
	}

	for _, name := range config.Builtins() {
		if !cfg.Enabled(name) {
			t.Errorf("built-in %s is disabled with no file", name)
		}
	}
}

func TestLoad_ReadsEveryKey(t *testing.T) {
	t.Parallel()

	root := write(t, `
max_rounds = 5
disable = ["lint"]

[[check]]
name = "ruff"
globs = ["**/*.py"]
command = "uv run ruff check {files}"
fix = "uv run ruff check --fix {files}"
level = "advise"
stage = "edit"
timeout = "90s"

[[check]]
name = "docs"
globs = ["docs/**"]
command = "make docs"
`)

	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.MaxRounds != 5 || cfg.Enabled(config.BuiltinLint) || !cfg.Enabled(config.BuiltinTest) {
		t.Errorf("top-level keys not carried: %+v", cfg)
	}

	if len(cfg.Checks) != 2 {
		t.Fatalf("got %d checks, want 2", len(cfg.Checks))
	}

	got := cfg.Checks[0].Select([]string{"a.py", "src/b.py", "c.go"})
	if !slices.Equal(got, []string{"a.py", "src/b.py"}) {
		t.Errorf("Select = %v", got)
	}

	wants := []config.Check{
		{
			Name: "ruff", Command: "uv run ruff check {files}", Fix: "uv run ruff check --fix {files}",
			Level: config.LevelAdvise, Stage: config.StageEdit, Timeout: 90 * time.Second,
		},
		{
			Name: "docs", Command: "make docs",
			Level: config.LevelBlock, Stage: config.StageStop, Timeout: config.DefaultTimeout,
		},
	}

	for i, want := range wants {
		got := cfg.Checks[i]
		got.Globs = nil

		if !reflect.DeepEqual(got, want) {
			t.Errorf("check %d = %+v, want %+v", i, got, want)
		}
	}
}

func TestLoad_RejectsWhatItCannotHonor(t *testing.T) {
	t.Parallel()

	const check = "[[check]]\nname = \"x\"\nglobs = [\"*.py\"]\ncommand = \"true\"\n"

	tests := []struct {
		name string
		body string
		key  string
	}{
		{"unknown top-level key", "max_round = 3\n", "max_round"},
		{"unknown check key", check + "severity = \"block\"\n", "check.severity"},
		{"unknown level", check + "level = \"warn\"\n", `check "x" level`},
		{"unknown stage", check + "stage = \"commit\"\n", `check "x" stage`},
		{"unparsable timeout", check + "timeout = \"soon\"\n", `check "x" timeout`},
		{"negative timeout", check + "timeout = \"-1s\"\n", `check "x" timeout`},
		{"empty command", "[[check]]\nname = \"x\"\nglobs = [\"*.py\"]\ncommand = \" \"\n", `check "x" command`},
		{"missing name", "[[check]]\nglobs = [\"*.py\"]\ncommand = \"true\"\n", "check[0].name"},
		{"missing globs", "[[check]]\nname = \"x\"\ncommand = \"true\"\n", `check "x" globs`},
		{"bad glob", "[[check]]\nname = \"x\"\nglobs = [\"[a\"]\ncommand = \"true\"\n", `check "x" globs`},
		{"duplicate name", check + check, `check "x" name`},
		{"built-in name still enabled", strings.ReplaceAll(check, `"x"`, `"lint"`), `check "lint" name`},
		{"disabling something that is not a built-in", "disable = [\"ruff\"]\n", "disable"},
		{"zero rounds", "max_rounds = 0\n", "max_rounds"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := write(t, tt.body)

			_, err := config.Load(root)

			var invalid *config.Error
			if !errors.As(err, &invalid) {
				t.Fatalf("got %v, want a *config.Error", err)
			}

			if invalid.Key != tt.key {
				t.Errorf("error names key %q, want %q", invalid.Key, tt.key)
			}

			if !strings.Contains(err.Error(), filepath.Join(root, config.FileName)) {
				t.Errorf("error does not name the file: %v", err)
			}
		})
	}
}

func TestLoad_ADisabledBuiltinCanBeReplaced(t *testing.T) {
	t.Parallel()

	root := write(t, "disable = [\"test\"]\n[[check]]\nname = \"test\"\nglobs = [\"*.go\"]\ncommand = \"make test\"\n")

	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.Checks) != 1 || cfg.Enabled(config.BuiltinTest) {
		t.Errorf("got %+v", cfg)
	}
}

func TestLoad_ReportsBrokenTOMLAndUnreadableFiles(t *testing.T) {
	t.Parallel()

	if _, err := config.Load(write(t, "max_rounds = \n")); err == nil || !strings.Contains(err.Error(), "parsing") {
		t.Errorf("broken TOML: got %v", err)
	}

	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, config.FileName), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if _, err := config.Load(root); err == nil || !strings.Contains(err.Error(), "reading") {
		t.Errorf("directory in place of the file: got %v", err)
	}
}
