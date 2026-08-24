package tools_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/tool"
	"github.com/kyleking/wavez/internal/tools"
)

//nolint:nonamedreturns // gocritic's unnamedResult wants these two same-shaped results named
func recordingGate(t *testing.T, decision permission.Decision) (gate permission.GateFunc, consulted *bool) {
	t.Helper()

	var wasConsulted bool
	consulted = &wasConsulted
	gate = permission.GateFunc(func(_ context.Context, _ permission.Request) (permission.Decision, error) {
		wasConsulted = true

		return decision, nil
	})

	return gate, consulted
}

// TestShell_GuardRefusalNeverReachesGateOrExec proves the ordering
// invariant: a guard-refused command is turned away before the gate is
// asked and before sandbox.Exec runs. Root and sessionTmp point at
// directories that do not exist, so if execution were ever attempted,
// sandbox.Exec would fail to resolve them and Run would return a non-nil
// error instead of an IsError Result. Asserting err is nil and the content
// carries the guard's reason is what proves exec was never reached.
func TestShell_GuardRefusalNeverReachesGateOrExec(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "does-not-exist")
	gate, consulted := recordingGate(t, permission.Allow)

	sh := tools.NewShell(root, root, "thread-1", gate)
	result, err := sh.Run(context.Background(), mustJSON(t, map[string]any{"command": "rm -rf /"}))
	if err != nil {
		t.Fatalf("Run returned an error (exec was reached): %v", err)
	}
	if *consulted {
		t.Errorf("gate was consulted for a guard-refused command, want it skipped entirely")
	}
	if !result.IsError {
		t.Fatalf("IsError = false, want true for a refused command: %q", result.Content)
	}
	if !strings.Contains(result.Content, "refused") {
		t.Errorf("Content = %q, want it to say refused", result.Content)
	}
}

func TestShell_NeedsApprovalConsultsGateAndDenyBlocksExec(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "does-not-exist")
	gate, consulted := recordingGate(t, permission.Deny)

	sh := tools.NewShell(root, root, "thread-1", gate)
	result, err := sh.Run(context.Background(), mustJSON(t, map[string]any{"command": "rm -rf $TARGET"}))
	if err != nil {
		t.Fatalf("Run returned an error (exec was reached): %v", err)
	}
	if !*consulted {
		t.Errorf("gate was not consulted for a needs-approval command")
	}
	if !result.IsError {
		t.Fatalf("IsError = false, want true for a denied command: %q", result.Content)
	}
	if !strings.Contains(result.Content, "denied") {
		t.Errorf("Content = %q, want it to say denied", result.Content)
	}
}

func TestShell_AllowRunsWithoutConsultingGate(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sessionTmp := t.TempDir()
	gate, consulted := recordingGate(t, permission.Allow)

	sh := tools.NewShell(root, sessionTmp, "thread-1", gate)
	result, err := sh.Run(context.Background(), mustJSON(t, map[string]any{"command": "echo hello"}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if *consulted {
		t.Errorf("gate was consulted for an allow-verdict command, want it skipped")
	}
	if result.IsError {
		t.Fatalf("IsError = true, want false: %q", result.Content)
	}
	if !strings.Contains(result.Content, "exit code: 0") {
		t.Errorf("Content = %q, want it to report exit code 0", result.Content)
	}
	if !strings.Contains(result.Content, "hello") {
		t.Errorf("Content = %q, want it to contain the command's stdout", result.Content)
	}
}

func TestShell_ReportsNonzeroExitCode(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sessionTmp := t.TempDir()
	gate, _ := recordingGate(t, permission.Allow)

	sh := tools.NewShell(root, sessionTmp, "thread-1", gate)
	result, err := sh.Run(context.Background(), mustJSON(t, map[string]any{"command": "exit 7"}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(result.Content, "exit code: 7") {
		t.Errorf("Content = %q, want it to report exit code 7", result.Content)
	}
}

func TestShell_TrimsLongOutput(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sessionTmp := t.TempDir()
	gate, _ := recordingGate(t, permission.Allow)

	sh := tools.NewShell(root, sessionTmp, "thread-1", gate)
	result, err := sh.Run(context.Background(), mustJSON(t, map[string]any{"command": "seq 1 200"}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(result.Content, "lines omitted") {
		t.Errorf("Content did not mention omitted lines for 200 lines of output: %q", result.Content)
	}
	if strings.Contains(result.Content, "\n100\n") {
		t.Errorf("Content contains a middle line that should have been trimmed")
	}
	if !strings.Contains(result.Content, "\n1\n") || !strings.Contains(result.Content, "200") {
		t.Errorf("Content = %q, want the first and last lines kept", result.Content)
	}
}

// stubChecks is a Checks that answers however a case needs it to.
type stubChecks struct {
	status string
	known  bool
}

func (s stubChecks) Status() (string, bool) { return s.status, s.known }

// The system prompt has told runs not to re-run the project's checks since
// the gates shipped, and 37 of 278 logged shell calls did it anyway. What
// the harness knows, it answers.
func TestShellAnswersAGateItAlreadyRan(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	tests := []struct {
		name    string
		command string
		want    string
		checks  stubChecks
		ran     bool
	}{
		{
			name:    "a module sweep is answered from what the gates found",
			command: "go test ./...",
			checks:  stubChecks{status: "they ran on your changes and passed: go-test", known: true},
			want:    "Not run: this runs tests",
		},
		{
			name:    "one package's tests still run",
			command: "go test ./internal/edit/...",
			checks:  stubChecks{status: "they ran on your changes and passed: go-test", known: true},
			ran:     true,
		},
		{
			name:    "a sweep runs when the harness knows nothing yet",
			command: "go test ./...",
			checks:  stubChecks{},
			ran:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gate, _ := recordingGate(t, permission.Allow)
			sh := tools.NewShell(root, t.TempDir(), "t", gate, tools.WithChecks(tt.checks))

			res, err := sh.Run(t.Context(), mustJSON(t, map[string]any{"command": tt.command}))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			answered := strings.Contains(res.Content, "Not run:")
			if answered == tt.ran {
				t.Fatalf("ran = %v, want %v:\n%s", !answered, tt.ran, res.Content)
			}

			if tt.want != "" && !strings.Contains(res.Content, tt.want) {
				t.Errorf("missing %q:\n%s", tt.want, res.Content)
			}
		})
	}
}

// stubChanges answers with a fixed change set.
type stubChanges struct {
	changes []tool.Change
}

func (s stubChanges) Changed() []tool.Change { return s.changes }

// 24 of 278 logged shell calls asked jj or git what the run had changed,
// which the harness recorded as it changed it.
func TestShellAnswersWhatTheRunChanged(t *testing.T) {
	t.Parallel()

	changes := stubChanges{changes: []tool.Change{
		{Path: "a.go", Added: 3, Removed: 1},
		{Path: "a.go", Added: 2, Removed: 0},
		{Path: "b_test.go", Added: 9},
	}}

	tests := []struct {
		name    string
		command string
		want    string
	}{
		{name: "a status is answered", command: "jj st", want: "a.go (+5/-1)"},
		{name: "a diff is answered too", command: "git diff", want: "b_test.go (+9/-0)"},
		{name: "a write is not this tool's business", command: "jj commit -m x", want: ""},
		{name: "another command is untouched", command: "echo hi", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gate, _ := recordingGate(t, permission.Allow)
			sh := tools.NewShell(t.TempDir(), t.TempDir(), "t", gate, tools.WithChanges(changes))

			res, err := sh.Run(t.Context(), mustJSON(t, map[string]any{"command": tt.command}))
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			if tt.want == "" {
				if strings.Contains(res.Content, "Not run:") {
					t.Fatalf("answered a command it should have run:\n%s", res.Content)
				}

				return
			}

			if !strings.Contains(res.Content, tt.want) {
				t.Errorf("missing %q:\n%s", tt.want, res.Content)
			}
		})
	}
}
