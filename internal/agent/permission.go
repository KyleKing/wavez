package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/tool"
)

// PermissionRequester is implemented by a tool.Tool that can decide, for one
// call's input, whether it needs approval and what to ask. The risk class the
// tool declares decides whether the gate is consulted at all; this interface
// decides what the consult asks, because only the tool knows what its input
// means — shell keys an approval on the whole command line and web_fetch on
// the host.
type PermissionRequester interface {
	// RequestPermission reports whether input needs approval and, if so, the
	// permission.Request describing it.
	RequestPermission(input json.RawMessage) (permission.Request, bool)
}

// gateKeeper asks a permission.Gate once per distinct Request.Key per thread
// and remembers an AllowAlways answer for the rest of its lifetime. It is not
// safe for concurrent use; one Loop.Run call owns one gateKeeper.
//
// It is the one place on the dispatch path that reads a tool's declared risk
// class, so a tool that declares a risky class cannot reach Run without
// passing through here.
type gateKeeper struct {
	gate    permission.Gate
	allowed map[string]struct{}
	// gatedWrites is whether write_local calls are asked about. It is off by
	// default: the measurement says 5.7 write calls a run against 2.0
	// distinct files, so a write class keyed per file is two prompts a run,
	// and turning it on is a separate decision from declaring the class.
	gatedWrites bool
}

func newGateKeeper(gate permission.Gate, gatedWrites bool) *gateKeeper {
	return &gateKeeper{gate: gate, gatedWrites: gatedWrites, allowed: make(map[string]struct{})}
}

// check consults the gate for every class but read, and returns true when the
// call may proceed.
//
// The order is the order shell has always used: a tool's own refusal verdicts
// (guard classification, path containment) happen inside Run after this
// returns, so a Refuse verdict stays unreachable regardless of what the gate
// would have said.
func (g *gateKeeper) check(ctx context.Context, t tool.Tool, input json.RawMessage) (bool, error) {
	// A tool's own request is put to the gate whatever class it declares. A
	// class may add a gate and may never take one away, because a constant
	// that silences an explicit request is a bypass written as a
	// declaration.
	if requester, ok := t.(PermissionRequester); ok {
		if req, needed := requester.RequestPermission(input); needed {
			return g.ask(ctx, req)
		}
	}

	// What is left is write_local, the edit tools, which ask nothing of
	// their own. Every other class either asks for itself or mediates its
	// own interruption (question, annotate, look), which is that tool's UX
	// rather than a permission decision.
	if t.Risk() != tool.RiskWriteLocal || !g.gatedWrites {
		return true, nil
	}

	for _, path := range writePaths(input) {
		ok, err := g.ask(ctx, permission.Request{
			Tool: t.Name(), Action: "write", Detail: path, Key: "write " + path,
			Reason: "the write gate is enabled for this run",
		})
		if err != nil || !ok {
			return ok, err
		}
	}

	return true, nil
}

// ask puts one request to the gate, remembering an AllowAlways answer for its
// key.
func (g *gateKeeper) ask(ctx context.Context, req permission.Request) (bool, error) {
	if _, already := g.allowed[req.Key]; already {
		return true, nil
	}

	decision, err := g.gate.Ask(ctx, req)
	if err != nil {
		return false, fmt.Errorf("asking permission: %w", err)
	}

	switch decision {
	case permission.Allow:
		return true, nil
	case permission.AllowAlways:
		g.allowed[req.Key] = struct{}{}

		return true, nil
	case permission.Deny:
		return false, nil
	default:
		return false, nil
	}
}

// writePaths lists the files a write-class input names, so an approval can be
// keyed per file. It reads the "path" and "paths" fields every edit tool
// takes; an input naming neither is not asked about, which leaves such a tool
// ungated exactly as it is today.
func writePaths(input json.RawMessage) []string {
	var in struct {
		Path  string   `json:"path"`
		Paths []string `json:"paths"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return nil
	}

	out := make([]string, 0, len(in.Paths)+1)
	seen := make(map[string]bool, len(in.Paths)+1)
	for _, p := range append([]string{in.Path}, in.Paths...) {
		if p = strings.TrimSpace(p); p == "" || seen[p] {
			continue
		}

		seen[p] = true
		out = append(out, p)
	}

	return out
}
