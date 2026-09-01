package agent

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/tool"
)

// riskTool is a Tool whose risk class and permission request are set per test.
type riskTool struct {
	risk   tool.RiskClass
	req    permission.Request
	needed bool
}

func (*riskTool) Name() string            { return "risk" }
func (*riskTool) Description() string     { return "test double" }
func (*riskTool) Schema() json.RawMessage { return json.RawMessage(`{}`) }
func (t *riskTool) Risk() tool.RiskClass  { return t.risk }
func (*riskTool) Run(context.Context, json.RawMessage) (tool.Result, error) {
	return tool.Result{Content: "ran"}, nil
}

func (t *riskTool) RequestPermission(json.RawMessage) (permission.Request, bool) {
	return t.req, t.needed
}

// plainTool declares a risk class and nothing else: no RequestPermission, so
// it exercises the keeper's own write_local handling the way the edit tools
// reach it.
type plainTool struct {
	risk tool.RiskClass
}

func (*plainTool) Name() string            { return "plain" }
func (*plainTool) Description() string     { return "test double" }
func (*plainTool) Schema() json.RawMessage { return json.RawMessage(`{}`) }
func (t *plainTool) Risk() tool.RiskClass  { return t.risk }
func (*plainTool) Run(context.Context, json.RawMessage) (tool.Result, error) {
	return tool.Result{Content: "ran"}, nil
}

// countingGate records each request it is asked.
type countingGate struct {
	deny string
	keys []string
}

func (c *countingGate) Ask(_ context.Context, req permission.Request) (permission.Decision, error) {
	c.keys = append(c.keys, req.Key)
	if req.Key == c.deny {
		return permission.Deny, nil
	}

	return permission.Allow, nil
}

// checked runs one call through a keeper built over g and reports what the
// keeper answered.
func checked(g *countingGate, gatedWrites bool, t tool.Tool, in string) (bool, error) {
	return newGateKeeper(g, gatedWrites).check(context.Background(), t, json.RawMessage(in))
}

// wantAsks fails unless the gate was asked for exactly these keys, in order.
func wantAsks(t *testing.T, g *countingGate, keys ...string) {
	t.Helper()

	if !slices.Equal(g.keys, keys) {
		t.Fatalf("gate asked %v; want %v", g.keys, keys)
	}
}

// wantProceeds fails unless the keeper let the call through without error.
func wantProceeds(t *testing.T, ok bool, err error) {
	t.Helper()

	if err != nil || !ok {
		t.Fatalf("check = %v, %v; want true, nil", ok, err)
	}
}

// TestGateKeeperChecksByRiskClass covers the one place on the dispatch path
// that reads a tool's declared risk class. A class adds a gate and never
// takes one away: a tool asking for itself is asked whatever it declares,
// and the class is what reaches a write-class tool that asks nothing, only
// when the keeper was built to gate writes. The shell's own order is
// unchanged, since the keeper still runs before Run and a Refuse verdict is
// still decided inside Run.
func TestGateKeeperChecksByRiskClass(t *testing.T) {
	t.Parallel()

	t.Run("read that asks nothing is never asked", func(t *testing.T) {
		t.Parallel()

		g := &countingGate{}
		ok, err := checked(g, true, &plainTool{risk: tool.RiskRead}, `{"path":"a.go"}`)
		wantProceeds(t, ok, err)
		wantAsks(t, g)
	})

	// A class that could silence a tool's own request would be a bypass
	// written as a declaration, so the request wins and the class only adds.
	t.Run("a declared class cannot silence the tool's own request", func(t *testing.T) {
		t.Parallel()

		g := &countingGate{}
		tt := &riskTool{risk: tool.RiskRead, req: permission.Request{Key: "k"}, needed: true}
		ok, err := checked(g, false, tt, `{}`)
		wantProceeds(t, ok, err)
		wantAsks(t, g, "k")
	})

	t.Run("exec consults the gate through the tool's request", func(t *testing.T) {
		t.Parallel()

		g := &countingGate{}
		tt := &riskTool{risk: tool.RiskExec, req: permission.Request{Key: "k"}, needed: true}
		ok, err := checked(g, false, tt, `{}`)
		wantProceeds(t, ok, err)
		wantAsks(t, g, "k")
	})

	t.Run("deny refuses without reaching Run", func(t *testing.T) {
		t.Parallel()

		tt := &riskTool{risk: tool.RiskExec, req: permission.Request{Key: "deny"}, needed: true}
		if ok, err := checked(&countingGate{deny: "deny"}, false, tt, `{}`); err != nil || ok {
			t.Fatalf("check = %v, %v; want false, nil", ok, err)
		}
	})

	t.Run("a tool that needs nothing is never asked", func(t *testing.T) {
		t.Parallel()

		g := &countingGate{}
		ok, err := checked(g, false, &riskTool{risk: tool.RiskExec}, `{}`)
		wantProceeds(t, ok, err)
		wantAsks(t, g)
	})

	t.Run("write_local is off by default", func(t *testing.T) {
		t.Parallel()

		g := &countingGate{}
		ok, err := checked(g, false, &plainTool{risk: tool.RiskWriteLocal}, `{"path":"a.go"}`)
		wantProceeds(t, ok, err)
		wantAsks(t, g)
	})

	t.Run("write_local asks once per distinct file when enabled", func(t *testing.T) {
		t.Parallel()

		g := &countingGate{}
		tt := &plainTool{risk: tool.RiskWriteLocal}
		ok, err := checked(g, true, tt, `{"path":"a.go","paths":["b.go","b.go"]}`)
		wantProceeds(t, ok, err)
		wantAsks(t, g, "write a.go", "write b.go")
	})

	t.Run("a denied file stops the write before Run", func(t *testing.T) {
		t.Parallel()

		tt := &plainTool{risk: tool.RiskWriteLocal}
		ok, err := checked(&countingGate{deny: "write b.go"}, true, tt, `{"paths":["a.go","b.go"]}`)
		if err != nil || ok {
			t.Fatalf("check = %v, %v; want false, nil", ok, err)
		}
	})
}
