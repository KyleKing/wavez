package hook_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/hook"
	"github.com/kyleking/wavez/internal/tool"
)

const scriptPerm = 0o755

// testTimeout bounds every subtest that is not itself exercising the timeout
// path. DefaultTimeout is a production value sized for one hook process, not
// for a busy CI or dev machine running these tests alongside a full build, so
// asserting exit-code handling here must not race it.
const testTimeout = 30 * time.Second

// writeScript writes body as an executable /bin/sh script and returns its path.
func writeScript(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "hook.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), scriptPerm); err != nil {
		t.Fatalf("writing hook script: %v", err)
	}

	return path
}

func TestPreToolUse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		script      string
		wantVerdict hook.Verdict
		wantReason  string
		timeout     time.Duration
		missing     bool
		unset       bool
	}{
		{
			name:        "not configured allows without running anything",
			unset:       true,
			wantVerdict: hook.Allow,
		},
		{
			name:        "exit zero allows",
			script:      "exit 0",
			wantVerdict: hook.Allow,
		},
		{
			name:        "exit two refuses with the hook's own reason",
			script:      `echo "writes outside src/ are policy-blocked"; exit 2`,
			wantVerdict: hook.Refuse,
			wantReason:  "writes outside src/ are policy-blocked",
		},
		{
			name:        "exit two with no output still names the tool",
			script:      "exit 2",
			wantVerdict: hook.Refuse,
			wantReason:  `pre-tool-use hook refused "edit"`,
		},
		{
			name:        "other nonzero exit refuses as a failed hook",
			script:      `echo boom >&2; exit 7`,
			wantVerdict: hook.Refuse,
			wantReason:  "pre-tool-use hook exited 7",
		},
		{
			name:        "a hook that hangs refuses when the bound trips",
			script:      "sleep 30",
			timeout:     20 * time.Millisecond,
			wantVerdict: hook.Refuse,
			wantReason:  "pre-tool-use hook timed out after 20ms",
		},
		{
			name:        "a missing executable refuses",
			missing:     true,
			wantVerdict: hook.Refuse,
			wantReason:  "pre-tool-use hook could not run",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()

			var opts []hook.Option

			switch {
			case tc.unset:
			case tc.missing:
				opts = append(opts, hook.WithPreToolUse(filepath.Join(dir, "absent-hook")))
			default:
				opts = append(opts, hook.WithPreToolUse(writeScript(t, tc.script)))
			}

			timeout := tc.timeout
			if timeout == 0 {
				timeout = testTimeout
			}
			opts = append(opts, hook.WithTimeout(timeout))

			runner := hook.New(dir, opts...)

			got := runner.PreToolUse(t.Context(), hook.Call{
				ThreadID: "t1",
				Tool:     "edit",
				Input:    json.RawMessage(`{"path":"main.go"}`),
				Paths:    []string{"main.go"},
			})

			if got.Verdict != tc.wantVerdict {
				t.Errorf("Verdict = %q, want %q (reason %q, output %q)",
					got.Verdict, tc.wantVerdict, got.Reason, got.Output)
			}
			if tc.wantReason != "" && !strings.Contains(got.Reason, tc.wantReason) {
				t.Errorf("Reason = %q, want it to contain %q", got.Reason, tc.wantReason)
			}
			if tc.wantVerdict == hook.Allow && got.Reason != "" {
				t.Errorf("Reason = %q, want empty on an allow", got.Reason)
			}
		})
	}
}

func TestPostToolUse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		script     string
		wantReason string
		timeout    time.Duration
		unset      bool
		wantOK     bool
	}{
		{name: "not configured is a no-op", unset: true, wantOK: true},
		{name: "exit zero is ok", script: "exit 0", wantOK: true},
		{name: "nonzero exit is reported, never fatal", script: "exit 3", wantReason: "post-tool-use hook exited 3"},
		{
			name:       "a hook that hangs is reported when the bound trips",
			script:     "sleep 30",
			timeout:    20 * time.Millisecond,
			wantReason: "post-tool-use hook timed out after 20ms",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var opts []hook.Option
			if !tc.unset {
				opts = append(opts, hook.WithPostToolUse(writeScript(t, tc.script)))
			}

			timeout := tc.timeout
			if timeout == 0 {
				timeout = testTimeout
			}
			opts = append(opts, hook.WithTimeout(timeout))

			runner := hook.New(t.TempDir(), opts...)

			got := runner.PostToolUse(t.Context(),
				hook.Call{Tool: "edit", Input: json.RawMessage(`{}`)},
				tool.Result{Content: "ok"})

			if got.OK != tc.wantOK {
				t.Errorf("OK = %v, want %v (reason %q)", got.OK, tc.wantOK, got.Reason)
			}
			if tc.wantReason != "" && !strings.Contains(got.Reason, tc.wantReason) {
				t.Errorf("Reason = %q, want it to contain %q", got.Reason, tc.wantReason)
			}
		})
	}
}

// TestPayloadOnStdin drives a real script that captures stdin, so the wire
// shape a user's hook reads is asserted against what a hook actually receives.
func TestPayloadOnStdin(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	capture := filepath.Join(dir, "payload.json")
	script := writeScript(t, `cat > "$1"`)
	runner := hook.New(dir,
		hook.WithPreToolUse(script, capture),
		hook.WithPostToolUse(script, capture),
		hook.WithTimeout(testTimeout))

	call := hook.Call{
		ThreadID: "thread-7",
		Tool:     "edit",
		Input:    json.RawMessage(`{"path":"main.go","old":"a"}`),
		Paths:    []string{"main.go", "go.mod"},
	}

	if got := runner.PreToolUse(t.Context(), call); got.Verdict != hook.Allow {
		t.Fatalf("PreToolUse = %+v, want allow", got)
	}

	pre := readPayload(t, capture)
	if pre.Event != hook.EventPreToolUse {
		t.Errorf("Event = %q, want %q", pre.Event, hook.EventPreToolUse)
	}
	if pre.ThreadID != call.ThreadID || pre.Tool != call.Tool {
		t.Errorf("payload = %+v, want thread %q tool %q", pre, call.ThreadID, call.Tool)
	}
	if string(pre.Input) != string(call.Input) {
		t.Errorf("Input = %s, want %s", pre.Input, call.Input)
	}
	if strings.Join(pre.Paths, ",") != "main.go,go.mod" {
		t.Errorf("Paths = %v, want [main.go go.mod]", pre.Paths)
	}
	if pre.Result != nil {
		t.Errorf("Result = %+v, want nil before the tool ran", pre.Result)
	}

	result := tool.Result{
		Content: "patched",
		Changes: []tool.Change{{Path: "main.go", Added: 2, Removed: 1}},
		IsError: true,
	}
	if got := runner.PostToolUse(t.Context(), call, result); !got.OK {
		t.Fatalf("PostToolUse = %+v, want ok", got)
	}

	post := readPayload(t, capture)
	if post.Event != hook.EventPostToolUse {
		t.Errorf("Event = %q, want %q", post.Event, hook.EventPostToolUse)
	}
	if post.Result == nil {
		t.Fatal("Result = nil, want the tool result")
	}
	if post.Result.Content != result.Content || !post.Result.IsError {
		t.Errorf("Result = %+v, want %q and is_error true", post.Result, result.Content)
	}
	if len(post.Result.Changes) != 1 || post.Result.Changes[0].Path != "main.go" {
		t.Errorf("Result.Changes = %+v, want the one change on main.go", post.Result.Changes)
	}
}

// TestNotConfigured covers the common case: no command, so no process and no
// captured output on either method.
func TestNotConfigured(t *testing.T) {
	t.Parallel()

	runner := hook.New(t.TempDir())

	if runner.PreToolUseConfigured() || runner.PostToolUseConfigured() {
		t.Fatal("a Runner with no options reports a hook configured")
	}

	call := hook.Call{Tool: "edit", Input: json.RawMessage(`{}`)}
	if got := runner.PreToolUse(t.Context(), call); got.Verdict != hook.Allow || got.Output != "" {
		t.Errorf("PreToolUse = %+v, want a bare allow", got)
	}
	if got := runner.PostToolUse(t.Context(), call, tool.Result{}); !got.OK || got.Output != "" {
		t.Errorf("PostToolUse = %+v, want a bare ok", got)
	}
}

func readPayload(t *testing.T, path string) hook.Payload {
	t.Helper()

	raw, err := os.ReadFile(path) //nolint:gosec // path is built by this test
	if err != nil {
		t.Fatalf("reading captured payload: %v", err)
	}

	var payload hook.Payload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decoding captured payload %q: %v", raw, err)
	}

	return payload
}
