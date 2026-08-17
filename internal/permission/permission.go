// Package permission defines the approval gate in front of anything a tool
// cannot make unreachable by sandboxing alone.
package permission

import "context"

// Decision is the answer to one Request.
type Decision string

const (
	// Allow permits this one action.
	Allow Decision = "allow"
	// Deny refuses this one action.
	Deny Decision = "deny"
	// AllowAlways permits this action and every later one matching its Key,
	// for the lifetime of the thread.
	AllowAlways Decision = "allow_always"
)

// Request describes an action awaiting approval.
type Request struct {
	ThreadID string `json:"thread_id"`
	Tool     string `json:"tool"`
	Action   string `json:"action"`
	Detail   string `json:"detail,omitempty"`
	// Reason is why approval is needed, from the deterministic checker that
	// decided to ask. It says what the client cannot derive from Detail: a
	// command reads as ordinary until something names the pattern in it.
	Reason string `json:"reason,omitempty"`
	// Key groups actions that one AllowAlways covers, such as a command name.
	Key   string   `json:"key"`
	Paths []string `json:"paths,omitempty"`
}

// Gate decides whether an action may run. Model output is never a Gate input:
// a decision comes from a deterministic checker or from the user.
type Gate interface {
	Ask(ctx context.Context, req Request) (Decision, error)
}

// GateFunc adapts a function to Gate.
type GateFunc func(ctx context.Context, req Request) (Decision, error)

// Ask implements Gate.
func (f GateFunc) Ask(ctx context.Context, req Request) (Decision, error) { return f(ctx, req) }

// AllowAll is a Gate for headless runs that have already accepted the risk.
func AllowAll() Gate { //nolint:ireturn // a Gate is what the caller wires in
	return GateFunc(func(context.Context, Request) (Decision, error) { return Allow, nil })
}

// DenyAll is a Gate for read-only threads such as plan mode.
func DenyAll() Gate { //nolint:ireturn // a Gate is what the caller wires in
	return GateFunc(func(context.Context, Request) (Decision, error) { return Deny, nil })
}
