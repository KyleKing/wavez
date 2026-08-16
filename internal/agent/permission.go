package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/tool"
)

// PermissionRequester is implemented by a tool.Tool that can decide, for one
// call's input, whether it needs approval and what to ask. A tool that does
// not implement it never goes through the gate. This keeps the decision of
// whether an action is destructive where the action is defined: the loop
// only asks what the tool tells it to ask, and only interprets the answer.
type PermissionRequester interface {
	// RequestPermission reports whether input needs approval and, if so, the
	// permission.Request describing it.
	RequestPermission(input json.RawMessage) (permission.Request, bool)
}

// gateKeeper asks a permission.Gate once per distinct Request.Key per thread
// and remembers an AllowAlways answer for the rest of its lifetime. It is not
// safe for concurrent use; one Loop.Run call owns one gateKeeper.
type gateKeeper struct {
	gate    permission.Gate
	allowed map[string]struct{}
}

func newGateKeeper(gate permission.Gate) *gateKeeper {
	return &gateKeeper{gate: gate, allowed: make(map[string]struct{})}
}

// check consults t if it is a PermissionRequester and the call needs
// approval. It returns true when the call may proceed.
func (g *gateKeeper) check(ctx context.Context, t tool.Tool, input json.RawMessage) (bool, error) {
	requester, ok := t.(PermissionRequester)
	if !ok {
		return true, nil
	}
	req, needed := requester.RequestPermission(input)
	if !needed {
		return true, nil
	}
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
