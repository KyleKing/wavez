package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/llm/fake"
	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/router"
	"github.com/kyleking/wavez/internal/tool"
)

var errCapture = errors.New("not a jj repository")

// stubCheckpointer scripts Capture and records every Restore call. RepoRoot
// resolves every path to root, which is what a single-repository project
// sees; roots overrides that per path.
type stubCheckpointer struct {
	captureErr error
	captured   string
	roots      map[string]string
	restores   []string
}

func (c *stubCheckpointer) RepoRoot(_ context.Context, path string) (string, error) {
	if repo, ok := c.roots[path]; ok {
		return repo, nil
	}

	return "/repo", nil
}

func (c *stubCheckpointer) Capture(context.Context, string) (string, error) {
	return c.captured, c.captureErr
}

func (c *stubCheckpointer) Restore(_ context.Context, _, checkpoint string) error {
	c.restores = append(c.restores, checkpoint)

	return nil
}

func TestRun_CapturesCheckpointOnOutcome(t *testing.T) {
	t.Parallel()

	local := fake.New("local", fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn})
	hosted := fake.New("hosted")

	th := newThread(t)
	reg := tool.NewRegistry(echoTool{name: "echo"})
	cp := &stubCheckpointer{captured: "op123"}
	loop := agent.New(tiers(local, hosted), reg, permission.AllowAll(), agent.WithCheckpointer(cp, "/repo"))

	out, err := loop.Run(context.Background(), th, basicPrefix(), "do it", router.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Checkpoint != "op123" {
		t.Fatalf("Outcome.Checkpoint = %q, want %q", out.Checkpoint, "op123")
	}
}

func TestRun_CheckpointCaptureFailureFailsRunOutright(t *testing.T) {
	t.Parallel()

	local := fake.New("local", fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn})
	hosted := fake.New("hosted")

	th := newThread(t)
	reg := tool.NewRegistry(echoTool{name: "echo"})
	cp := &stubCheckpointer{captureErr: errCapture}
	loop := agent.New(tiers(local, hosted), reg, permission.AllowAll(), agent.WithCheckpointer(cp, "/repo"))

	_, err := loop.Run(context.Background(), th, basicPrefix(), "do it", router.Input{})
	if err == nil {
		t.Fatal("Run: want an error when Capture fails, got nil")
	}
	if !errors.Is(err, errCapture) {
		t.Fatalf("Run error = %v, want it to wrap %v", err, errCapture)
	}
	if len(local.Requests()) != 0 {
		t.Fatal("Run called the model despite a failed checkpoint capture")
	}
}

func TestRun_FailurePathSurfacesCheckpointForRestore(t *testing.T) {
	t.Parallel()

	local := fake.New("local", fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn})
	hosted := fake.New("hosted")

	th := newThread(t)
	reg := tool.NewRegistry(changeTool{name: "changer", changes: []tool.Change{{Path: "a.go", Added: 1}}})
	cp := &stubCheckpointer{captured: "op-before-turn"}
	v := &stubVerifier{script: []verifyOutcome{{feedback: "build failed", ok: false}}}
	loop := agent.New(tiers(local, hosted), reg, permission.AllowAll(),
		agent.WithCheckpointer(cp, "/repo"), agent.WithVerifier(v), agent.WithMaxVerifyRounds(1))

	out, err := loop.Run(context.Background(), th, basicPrefix(), "do it", router.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.Stop != agent.StopVerifyFailed {
		t.Fatalf("Stop = %q, want verify_failed", out.Stop)
	}
	if out.Checkpoint != "op-before-turn" {
		t.Fatalf("Outcome.Checkpoint = %q, want %q", out.Checkpoint, "op-before-turn")
	}

	events, err := th.Log().Since(0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}

	var found bool

	for _, ev := range events {
		if ev.Kind != event.KindError {
			continue
		}

		if got, ok := ev.Detail["checkpoint"].(string); ok && got == "op-before-turn" {
			found = true
		}
	}

	if !found {
		t.Fatal("no failure event recorded the checkpoint a caller would restore to")
	}

	if len(cp.restores) != 0 {
		t.Fatal("Run must never restore on its own; that is the coordinator's call")
	}
}

func TestLoop_RestoreCheckpointDelegatesToConfiguredCheckpointer(t *testing.T) {
	t.Parallel()

	local := fake.New("local")
	hosted := fake.New("hosted")
	reg := tool.NewRegistry(echoTool{name: "echo"})
	cp := &stubCheckpointer{}
	loop := agent.New(tiers(local, hosted), reg, permission.AllowAll(), agent.WithCheckpointer(cp, "/repo"))

	if err := loop.RestoreCheckpoint(context.Background(), "op123"); err != nil {
		t.Fatalf("RestoreCheckpoint: %v", err)
	}
	if len(cp.restores) != 1 || cp.restores[0] != "op123" {
		t.Fatalf("restores = %v, want [op123]", cp.restores)
	}
}

func TestLoop_RestoreCheckpointWithNoCheckpointerErrors(t *testing.T) {
	t.Parallel()

	local := fake.New("local")
	hosted := fake.New("hosted")
	reg := tool.NewRegistry(echoTool{name: "echo"})
	loop := agent.New(tiers(local, hosted), reg, permission.AllowAll())

	if err := loop.RestoreCheckpoint(context.Background(), "op123"); err == nil {
		t.Fatal("RestoreCheckpoint: want an error with no Checkpointer configured")
	}
}

// countingCheckpointer hands out a distinct operation id per capture, the
// way jj does once the working copy has moved between them.
type countingCheckpointer struct {
	captures int
}

func (*countingCheckpointer) RepoRoot(_ context.Context, _ string) (string, error) {
	return "/repo", nil
}

func (c *countingCheckpointer) Capture(context.Context, string) (string, error) {
	c.captures++

	return fmt.Sprintf("op%d", c.captures), nil
}

func (*countingCheckpointer) Restore(context.Context, string, string) error { return nil }

// writingTool reports a change, which is what a checkpoint hangs on.
type writingTool struct {
	echoTool
	path string
}

func (w writingTool) Run(context.Context, json.RawMessage) (tool.Result, error) {
	return tool.Result{Content: "wrote " + w.path, Changes: []tool.Change{{Path: w.path, Added: 1}}}, nil
}

// A run's checkpoint used to be one operation id for the whole run, so undo
// was all-or-nothing. One per accepted change costs a jj command per edit
// (40-70 ms on this repo) and makes each edit reachable on its own.
func TestRun_CheckpointsEveryEditSeparately(t *testing.T) {
	t.Parallel()

	local := fake.New("local",
		fake.Turn{
			ToolCalls:  []llm.ToolCall{{ID: "1", Name: "write", Input: json.RawMessage(`{"n":1}`)}},
			StopReason: llm.StopToolUse,
		},
		fake.Turn{
			ToolCalls:  []llm.ToolCall{{ID: "2", Name: "write", Input: json.RawMessage(`{"n":2}`)}},
			StopReason: llm.StopToolUse,
		},
		fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn})
	hosted := fake.New("hosted")

	th := newThread(t)
	reg := tool.NewRegistry(writingTool{echoTool: echoTool{name: "write"}, path: "a.go"})
	cp := &countingCheckpointer{}

	out, err := agent.New(tiers(local, hosted), reg, permission.AllowAll(),
		agent.WithCheckpointer(cp, "/repo")).Run(context.Background(), th, basicPrefix(), "do it", router.Input{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(out.Edits) != 2 {
		t.Fatalf("Outcome.Edits = %v, want one point per accepted change", out.Edits)
	}

	if out.Edits[0].Op == out.Edits[1].Op {
		t.Errorf("both edits share operation %q, so neither is reachable on its own", out.Edits[0].Op)
	}

	if got := out.Edits[0].Paths; len(got) != 1 || got[0] != "a.go" {
		t.Errorf("Edits[0].Paths = %v, want the path the edit wrote", got)
	}

	if out.Checkpoint != "op1" {
		t.Errorf("Outcome.Checkpoint = %q, want the run's own capture to still come first", out.Checkpoint)
	}
}

// A fork inherits its parent's change set and none of its transcript, and a
// resumed thread's history starts empty, so both are threads whose goal is
// gone rather than merely far away. The loop restates it in those cases and
// stays silent when the history already carries it, which is what keeps a
// normal turn from paying for it.
func TestRun_RestatesTheGoalOnlyWhenTheHistoryLostIt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		seed    string
		goal    string
		restate bool
	}{
		{name: "a resumed thread has lost it", goal: "make the lease TTL configurable", restate: true},
		{
			name: "a thread that was just prompted has not",
			seed: "make the lease TTL configurable",
			goal: "make the lease TTL configurable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			th := newThread(t)
			if tt.seed != "" {
				if err := th.AppendUser(t.Context(), tt.seed); err != nil {
					t.Fatalf("AppendUser: %v", err)
				}
			}

			if err := th.SetGoal(t.Context(), tt.goal); err != nil {
				t.Fatalf("SetGoal: %v", err)
			}

			local := fake.New("local", fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn})
			loop := agent.New(tiers(local, fake.New("hosted")), tool.NewRegistry(echoTool{name: "echo"}),
				permission.AllowAll())

			if _, err := loop.Run(t.Context(), th, basicPrefix(), "carry on", router.Input{}); err != nil {
				t.Fatalf("Run: %v", err)
			}

			reqs := local.Requests()
			if len(reqs) == 0 {
				t.Fatal("the model was never called")
			}

			if got := strings.Contains(reqs[0].System, "## Goal"); got != tt.restate {
				t.Errorf("system carried the goal = %v, want %v:\n%s", got, tt.restate, reqs[0].System)
			}
		})
	}
}
