package agent_test

import (
	"context"
	"errors"
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

// stubCheckpointer scripts Capture and records every Restore call.
type stubCheckpointer struct {
	captured   string
	captureErr error
	restores   []string
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
