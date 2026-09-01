package tools_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

// An allow-always answer can outlive the thread that gave it, so the key it
// is remembered under has to name the action rather than the program: two
// different commands starting with the same word are two approvals.
func TestShell_ApprovalKeyNamesTheWholeCommand(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "does-not-exist")

	var keys []string

	gate := permission.GateFunc(func(_ context.Context, req permission.Request) (permission.Decision, error) {
		keys = append(keys, req.Key)

		return permission.Deny, nil
	})

	sh := tools.NewShell(root, root, "thread-1", gate)
	for _, cmd := range []string{"rm -rf  $A", "rm -rf $B"} {
		if _, err := sh.Run(context.Background(), mustJSON(t, map[string]any{"command": cmd})); err != nil {
			t.Fatalf("Run: %v", err)
		}
	}

	want := []string{"rm -rf $A", "rm -rf $B"}
	if len(keys) != len(want) || keys[0] != want[0] || keys[1] != want[1] {
		t.Fatalf("keys = %q, want %q", keys, want)
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
	covers []string
	known  bool
}

func (s stubChecks) Status(string) (string, bool) { return s.status, s.known }

func (s stubChecks) Covers(_ string, pkgs []string) bool {
	for _, p := range pkgs {
		if !slices.Contains(s.covers, p) {
			return false
		}
	}

	return len(pkgs) > 0
}

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
			name:    "a package the gates ran over is answered too",
			command: "go test ./internal/edit/...",
			checks: stubChecks{
				status: "they ran on your changes and passed: go-test",
				covers: []string{"internal/edit"},
				known:  true,
			},
			want: "Not run: this runs the tests of a package you changed",
		},
		{
			name:    "a package they never ran over still runs",
			command: "go test ./internal/edit",
			checks:  stubChecks{status: "they ran on your changes and passed: go-test", known: true},
			ran:     true,
		},
		{
			name:    "watching one failure still runs",
			command: "go test -run TestOne ./internal/edit",
			checks: stubChecks{
				status: "they ran on your changes and passed: go-test",
				covers: []string{"internal/edit"},
				known:  true,
			},
			ran: true,
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

func (s stubChanges) Changed(string) []tool.Change { return s.changes }

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

// A shell rewrite lands in no change set and no checkpoint, and it is the
// expensive spelling besides: one run spent seven shell calls putting two
// comment lines through `sed -i`.
func TestShell_RefusesAnEditMadeThroughAStreamEditor(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "f.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	gate, _ := recordingGate(t, permission.Allow)
	sh := tools.NewShell(root, t.TempDir(), "thread-1", gate)

	run := func(command string) tool.Result {
		t.Helper()

		result, err := sh.Run(context.Background(), mustJSON(t, map[string]any{"command": command}))
		if err != nil {
			t.Fatalf("Run(%s): %v", command, err)
		}

		return result
	}

	result := run("sed -i '' -e 's/before/after/' f.txt")
	if !result.IsError || !strings.Contains(result.Content, "str_replace") {
		t.Errorf("Content = %q (IsError=%v), want a refusal naming the tool that edits", result.Content, result.IsError)
	}

	//nolint:gosec // a fixture under t.TempDir()
	if body, err := os.ReadFile(path); err != nil || string(body) != "before\n" {
		t.Errorf("file = %q (err %v), want the edit not to have run", body, err)
	}

	// Reading with the same tool is not editing with it.
	if reading := run("sed -n '1,1p' f.txt"); reading.IsError || !strings.Contains(reading.Content, "before") {
		t.Errorf("a read-only sed was refused: %q", reading.Content)
	}
}

// Saying only how many lines were dropped tells a run that something is
// missing and gives it no way to get it, and the middle of a long test
// failure is where the assertion is.
func TestShell_KeepsTheWholeOutputOfATrimmedCommand(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	// The session directory lives under the project root in production,
	// which is what lets `read` reach what is kept there.
	session := filepath.Join(root, ".wavez", "sessions", "session-1")
	if err := os.MkdirAll(session, 0o700); err != nil {
		t.Fatalf("creating the session dir: %v", err)
	}

	gate, _ := recordingGate(t, permission.Allow)
	sh := tools.NewShell(root, session, "t", gate)

	res, err := sh.Run(t.Context(), mustJSON(t, map[string]any{"command": "seq 1 400"}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(res.Content, "lines omitted") {
		t.Fatalf("Content did not trim 400 lines:\n%s", res.Content)
	}

	_, rest, found := strings.Cut(res.Content, "the whole output is in ")
	if !found {
		t.Fatalf("the omission named no file to read:\n%s", res.Content)
	}

	rel, _, _ := strings.Cut(rest, ",")

	//nolint:gosec // the path comes from this test through the tool it is testing
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("reading the kept output at %s: %v", rel, err)
	}

	// 200 is in neither the head nor the tail the result kept.
	if !strings.Contains(string(body), "\n200\n") {
		t.Errorf("the kept output is missing the middle of the command's own output")
	}

	if !strings.Contains(res.Content, "\n1\n") || !strings.Contains(res.Content, "400") {
		t.Errorf("Content = %q, want the first and last lines still inline", res.Content)
	}
}

// TestShell_WriteOutsideTheSandboxNamesTheBoundary runs a real write into a
// declared extra directory. Seatbelt answers EPERM naming the file, which
// reads as that file's own permissions, and the lane that met it spent a
// turn before reaching for the edit tools.
func TestShell_WriteOutsideTheSandboxNamesTheBoundary(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skip("sandbox-exec not available on this host")
	}

	root, extra := t.TempDir(), t.TempDir()
	target := filepath.Join(extra, "AGENTS.md")

	if err := os.WriteFile(target, []byte("before\n"), 0o600); err != nil {
		t.Fatalf("seeding the extra directory: %v", err)
	}

	gate, _ := recordingGate(t, permission.Allow)
	sh := tools.NewShell(root, root, "thread-1", gate, tools.WithExtraRoots([]string{extra}))

	result, err := sh.Run(context.Background(), mustJSON(t, map[string]any{
		"command": "echo after > " + target,
	}))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !strings.Contains(result.Content, "writes only under the project root") {
		t.Errorf("Content = %q, want it to name the write boundary", result.Content)
	}
	if !strings.Contains(result.Content, "edit tools") {
		t.Errorf("Content = %q, want it to name the way in", result.Content)
	}

	got, err := os.ReadFile(target) //nolint:gosec // target is a t.TempDir() fixture
	if err != nil {
		t.Fatalf("reading back the target: %v", err)
	}
	if string(got) != "before\n" {
		t.Errorf("target = %q, want the sandbox to have denied the write", got)
	}
}
