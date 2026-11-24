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

	if cfg.Decide != (config.Decide{Timeout: config.DefaultDecideTimeout}) || cfg.Route {
		t.Errorf("got decide %+v route %v, want no model and no routing", cfg.Decide, cfg.Route)
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
		{"unknown decide key", "[decide]\nurl = \"http://localhost\"\n", "decide.url"},
		{"endpoint without a scheme", "[decide]\nendpoint = \"localhost:11434\"\nmodel = \"m\"\n", "decide.endpoint"},
		{"endpoint on another scheme", "[decide]\nendpoint = \"ftp://host/v1\"\nmodel = \"m\"\n", "decide.endpoint"},
		{"endpoint without a host", "[decide]\nendpoint = \"http://\"\nmodel = \"m\"\n", "decide.endpoint"},
		{"endpoint that does not parse", "[decide]\nendpoint = \"http://[::1\"\nmodel = \"m\"\n", "decide.endpoint"},
		{"endpoint without a model", "[decide]\nendpoint = \"http://localhost:11434/v1\"\n", "decide.model"},
		{"unparsable decide timeout", "[decide]\ntimeout = \"soon\"\n", "decide.timeout"},
		{"zero decide timeout", "[decide]\ntimeout = \"0s\"\n", "decide.timeout"},
		{"unparsable stop budget", "stop_budget = \"soon\"\n", "stop_budget"},
		{"zero stop budget", "stop_budget = \"0s\"\n", "stop_budget"},
		{"negative stop budget", "stop_budget = \"-2m\"\n", "stop_budget"},
		{"zero parallel", "parallel = 0\n", "parallel"},
		{"bad input glob", check + "inputs = [\"[a\"]\n", `check "x" inputs`},
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

func TestLoad_ReadsTheDecisionModel(t *testing.T) {
	t.Parallel()

	const endpoint = "http://localhost:11434/v1"

	tests := []struct {
		name  string
		body  string
		want  config.Decide
		route bool
	}{
		{"an empty file leaves both off", "", config.Decide{Timeout: config.DefaultDecideTimeout}, false},
		{"route alone", "route = true\n", config.Decide{Timeout: config.DefaultDecideTimeout}, true},
		{
			"an endpoint takes the default timeout",
			"[decide]\nendpoint = \"" + endpoint + "\"\nmodel = \"qwen2.5:3b\"\n",
			config.Decide{Endpoint: endpoint, Model: "qwen2.5:3b", Timeout: config.DefaultDecideTimeout},
			false,
		},
		{
			"every key",
			"route = true\n[decide]\nendpoint = \"https://models.example/v1\"\n" +
				"model = \"small\"\ntimeout = \"750ms\"\n",
			config.Decide{Endpoint: "https://models.example/v1", Model: "small", Timeout: 750 * time.Millisecond},
			true,
		},
		{
			"a model with no endpoint is carried and asks nothing",
			"[decide]\nmodel = \"small\"\n",
			config.Decide{Model: "small", Timeout: config.DefaultDecideTimeout},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := config.Load(write(t, tt.body))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			if cfg.Decide != tt.want || cfg.Route != tt.route {
				t.Errorf("got decide %+v route %v, want %+v and %v", cfg.Decide, cfg.Route, tt.want, tt.route)
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

func TestLoad_StopBudgetAndAnnounce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		body         string
		wantBudget   time.Duration
		wantAnnounce bool
	}{
		{"neither key set", "max_rounds = 2\n", 0, true},
		{"a budget", "stop_budget = \"2m30s\"\n", 150 * time.Second, true},
		{"a shorter budget", "stop_budget = \"90s\"\n", 90 * time.Second, true},
		{"announce off", "announce = false\n", 0, false},
		{"announce on", "announce = true\n", 0, true},
		{"both", "stop_budget = \"1m\"\nannounce = false\n", time.Minute, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := config.Load(write(t, tt.body))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			if cfg.StopBudget != tt.wantBudget || cfg.Announce != tt.wantAnnounce {
				t.Errorf("got budget %v announce %v, want %v and %v",
					cfg.StopBudget, cfg.Announce, tt.wantBudget, tt.wantAnnounce)
			}
		})
	}
}

func TestLoad_ParallelExclusiveAndInputs(t *testing.T) {
	t.Parallel()

	const check = "[[check]]\nname = \"x\"\nglobs = [\"*.py\"]\ncommand = \"true\"\n"

	tests := []struct {
		name          string
		body          string
		wantParallel  int
		wantExclusive bool
		wantCacheable bool
		wantInputs    int
	}{
		{name: "left out", body: check},
		{name: "a bound", body: "parallel = 3\n" + check, wantParallel: 3},
		{name: "an exclusive check", body: check + "exclusive = true\n", wantExclusive: true},
		{name: "no inputs beyond the globs", body: check + "inputs = []\n", wantCacheable: true},
		{
			name: "declared inputs", body: check + "inputs = [\"pyproject.toml\", \"uv.lock\"]\n",
			wantCacheable: true, wantInputs: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := config.Load(write(t, tt.body))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			got := cfg.Checks[0]
			if cfg.Parallel != tt.wantParallel || got.Exclusive != tt.wantExclusive ||
				got.Cacheable != tt.wantCacheable || len(got.Inputs) != tt.wantInputs {
				t.Errorf("got parallel %d exclusive %v cacheable %v inputs %d, want %d %v %v %d",
					cfg.Parallel, got.Exclusive, got.Cacheable, len(got.Inputs),
					tt.wantParallel, tt.wantExclusive, tt.wantCacheable, tt.wantInputs)
			}
		})
	}
}

func TestLoad_StopChecks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		want bool
	}{
		{"left out, the stop checks are off", "max_rounds = 2\n", false},
		{"turned on", "stop_checks = true\n", true},
		{"turned off", "stop_checks = false\n", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := config.Load(write(t, tt.body))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			if cfg.StopChecks != tt.want {
				t.Errorf("got stop checks %v, want %v", cfg.StopChecks, tt.want)
			}
		})
	}
}

func TestLoad_NoFileAnnounces(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !cfg.Announce || cfg.StopBudget != 0 {
		t.Errorf("got announce %v budget %v, want announce on and budget zero", cfg.Announce, cfg.StopBudget)
	}
}
