package gate_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/lsp"
	"github.com/kyleking/wavez/internal/lsp/lsptest"
	"github.com/kyleking/wavez/internal/tool"
)

func TestMain(m *testing.M) {
	lsptest.ServeIfChild()
	os.Exit(m.Run())
}

const lspTestBudget = 10 * time.Second

func lspProject(t *testing.T) string {
	t.Helper()

	root := t.TempDir()

	for _, name := range []string{"main.go", "other.go"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("package main\n"), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	return root
}

func lspRunContext(root string, files ...string) gate.RunContext {
	changes := make([]tool.Change, 0, len(files))
	for _, f := range files {
		changes = append(changes, tool.Change{Path: f})
	}

	return gate.RunContext{RepoRoot: root, Changes: changes, Selection: gate.Selection{Level: gate.LevelPackage}}
}

func runLSPGate(t *testing.T, root string, script lsptest.Script, rc gate.RunContext) gate.Result {
	t.Helper()

	pool := lsp.NewPool(root, lsptest.Server(t, script))

	ctx, cancel := context.WithTimeout(t.Context(), lspTestBudget)
	defer cancel()

	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), lspTestBudget)
		defer closeCancel()

		if err := pool.Close(closeCtx); err != nil {
			t.Errorf("closing pool: %v", err)
		}
	})

	result, err := gate.NewLSPGate(root, pool, gate.WithLSPTimeout(500*time.Millisecond)).Run(ctx, rc)
	if err != nil {
		t.Fatalf("running gate: %v", err)
	}

	return result
}

func TestLSPGate(t *testing.T) {
	t.Parallel()

	typeError := []lsptest.Diagnostic{
		{Message: `cannot use "one" (untyped string constant) as int value`, Severity: 1, Line: 8, Source: "compiler"},
	}

	tests := []struct {
		script       lsptest.Script
		name         string
		wantReason   string
		files        []string
		wantFrames   []string
		wantExamined int
		wantPass     bool
	}{
		{
			name:         "a clean file passes with nothing for the model",
			script:       lsptest.Script{},
			files:        []string{"main.go"},
			wantExamined: 1,
			wantPass:     true,
		},
		{
			name: "an error diagnostic reaches the model as a frame",
			script: lsptest.Script{
				Diagnostics: map[string][]lsptest.Diagnostic{"main.go": typeError},
			},
			files:        []string{"main.go"},
			wantExamined: 1,
			wantFrames:   []string{`main.go:8:1: cannot use "one" (untyped string constant) as int value [compiler]`},
		},
		{
			name: "warnings and hints stay out of the model's view",
			script: lsptest.Script{
				Diagnostics: map[string][]lsptest.Diagnostic{"main.go": {
					{Message: "unused parameter", Severity: 4, Line: 3, Source: "gopls"},
					{Message: "shadowed variable", Severity: 2, Line: 4, Source: "vet"},
				}},
			},
			files:        []string{"main.go"},
			wantExamined: 1,
			wantPass:     true,
		},
		{
			name: "a diagnostic for a file the change set never touched is dropped",
			script: lsptest.Script{
				Unrelated:   "other.go",
				Diagnostics: map[string][]lsptest.Diagnostic{"other.go": typeError},
			},
			files:        []string{"main.go"},
			wantExamined: 1,
			wantPass:     true,
		},
		{
			name:       "a change set with no file the server handles abstains",
			script:     lsptest.Script{},
			files:      []string{"README.md"},
			wantPass:   true,
			wantReason: "no changed file is one a configured language server handles",
		},
		{
			name:       "a server that publishes nothing is not a pass",
			script:     lsptest.Script{Mode: lsptest.ModeSilent},
			files:      []string{"main.go"},
			wantFrames: []string{"published no diagnostics for main.go"},
		},
		{
			name:       "a server that refuses this project is not a pass",
			script:     lsptest.Script{Mode: lsptest.ModeRefuseInitialize},
			files:      []string{"main.go"},
			wantFrames: []string{"did not start on this project"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := lspProject(t)
			result := runLSPGate(t, root, tc.script, lspRunContext(root, tc.files...))

			if result.Pass != tc.wantPass {
				t.Errorf("Pass = %v, want %v (reason %q, failures %+v)",
					result.Pass, tc.wantPass, result.Reason, result.Failures)
			}

			if result.Examined != tc.wantExamined {
				t.Errorf("Examined = %d, want %d", result.Examined, tc.wantExamined)
			}

			if tc.wantReason != "" && result.Reason != tc.wantReason {
				t.Errorf("Reason = %q, want %q", result.Reason, tc.wantReason)
			}

			assertFrames(t, result, tc.wantFrames)
		})
	}
}

func assertFrames(t *testing.T, result gate.Result, want []string) {
	t.Helper()

	if len(want) == 0 {
		if len(result.Failures) != 0 {
			t.Errorf("failures = %+v, want none", result.Failures)
		}

		return
	}

	if len(result.Failures) != 1 {
		t.Fatalf("failures = %+v, want exactly one", result.Failures)
	}

	frames := strings.Join(result.Failures[0].Frames, "\n")
	for _, w := range want {
		if !strings.Contains(frames, w) {
			t.Errorf("frames %q do not contain %q", frames, w)
		}
	}
}

func TestLSPGateReportsNoPassWhenTheServerIsNotInstalled(t *testing.T) {
	t.Parallel()

	root := lspProject(t)
	pool := lsp.NewPool(root, lsp.Server{
		Language: "go", Command: "wavez-no-such-language-server", Extensions: []string{".go"},
	})

	result, err := gate.NewLSPGate(root, pool).Run(t.Context(), lspRunContext(root, "main.go"))
	if err != nil {
		t.Fatalf("running gate: %v", err)
	}

	// A check that could not run has not passed, and installing a language
	// server is not work a run can do, so the reason stays in the log and
	// nothing reaches the model.
	if result.Pass {
		t.Error("a missing language server must not report a pass")
	}

	if len(result.Failures) != 0 {
		t.Errorf("failures = %+v, want none", result.Failures)
	}

	if !strings.Contains(result.Reason, "not found on PATH") {
		t.Errorf("Reason = %q, want it to name the missing binary", result.Reason)
	}
}

func TestLSPGateSkipsFilesTheChangeSetDeleted(t *testing.T) {
	t.Parallel()

	root := lspProject(t)
	rc := lspRunContext(root, "deleted.go")

	result := runLSPGate(t, root, lsptest.Script{}, rc)

	if !result.Pass || result.Examined != 0 {
		t.Errorf("result = %+v, want an abstention", result)
	}
}

// The gate against the real server, which is what the wiring will run.
func TestLSPGateAgainstGopls(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not installed")
	}

	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()

		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	write("go.mod", "module sample\n\ngo 1.25\n")
	write("main.go", "package main\n\nfunc Add(a, b int) int { return a + b }\n\n"+
		"func main() {\n\t_ = Add(\"one\", 2)\n}\n")

	pool := lsp.NewPool(root)

	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), lspTestBudget)
		defer closeCancel()

		if err := pool.Close(closeCtx); err != nil {
			t.Errorf("closing pool: %v", err)
		}
	})

	result, err := gate.NewLSPGate(root, pool, gate.WithLSPTimeout(30*time.Second)).
		Run(ctx, lspRunContext(root, "main.go"))
	if err != nil {
		t.Fatalf("running gate: %v", err)
	}

	if result.Pass {
		t.Fatalf("gopls found no error in a file that does not compile: %+v", result)
	}

	frames := strings.Join(result.Failures[0].Frames, "\n")
	if !strings.Contains(frames, "cannot use") {
		t.Errorf("frames = %q, want the type error", frames)
	}
}
