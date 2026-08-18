package routine

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/tool"
)

// Env is what every handler receives: the project it runs in and the change
// batch that triggered the run. A manual run carries no changes.
type Env struct {
	Root      string
	Changes   []tool.Change
	Selection gate.Selection
}

// Outcome is what one step produced. Failures are trimmed by the same rule
// gates use, so a routine's output reaches a reader no wider than a gate's.
type Outcome struct {
	Failures []gate.TrimmedFailure
	// Examined is how many units the step checked. A step that examined
	// nothing has abstained rather than passed, the distinction DESIGN.md's
	// Gates section requires of every check.
	Examined int
	Pass     bool
}

// Handler runs one bound step.
type Handler func(ctx context.Context, env Env) (Outcome, error)

// Bound is a step's action with its parameters already validated: the
// handler to call and the resource keys it holds while running.
type Bound struct {
	Run       Handler
	Resources []string
}

// Action is one named unit of work a step can invoke. Bind validates raw
// parameters from the config file and returns the handler for them, which
// is what makes a bad routine a config-load failure rather than a run-time
// one.
type Action struct {
	Bind func(params map[string]any) (Bound, error)
	Name string
}

// Registry holds the actions a project's routines may name.
type Registry struct {
	actions map[string]Action
}

// NewRegistry builds a Registry over actions. A later action of the same
// name replaces an earlier one, so a caller can override a built-in.
func NewRegistry(actions ...Action) *Registry {
	reg := &Registry{actions: make(map[string]Action, len(actions))}
	for _, a := range actions {
		reg.actions[a.Name] = a
	}

	return reg
}

// Lookup returns the action named name.
func (r *Registry) Lookup(name string) (Action, bool) {
	if r == nil {
		return Action{}, false
	}

	a, ok := r.actions[name]

	return a, ok
}

// Names lists every registered action, sorted.
func (r *Registry) Names() []string {
	if r == nil {
		return nil
	}

	out := make([]string, 0, len(r.actions))
	for name := range r.actions {
		out = append(out, name)
	}

	sort.Strings(out)

	return out
}

// Errors a validator returns for parameters an action cannot execute.
var (
	ErrMissingParam = errors.New("missing required parameter")
	ErrParamType    = errors.New("parameter has the wrong type")
	ErrUnknownParam = errors.New("unknown parameter")
	ErrEmptyParam   = errors.New("parameter is empty")
)

// stringList reads a list-of-strings parameter. A pkl Listing<String>
// arrives as []any, so the element check happens here rather than in a
// single type assertion.
func stringList(params map[string]any, key string) ([]string, error) {
	raw, ok := params[key]
	if !ok {
		return nil, fmt.Errorf("%w %q", ErrMissingParam, key)
	}

	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%w: %q wants a list of strings", ErrParamType, key)
	}

	out := make([]string, 0, len(list))

	for _, item := range list {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%w: %q wants a list of strings", ErrParamType, key)
		}

		out = append(out, s)
	}

	return out, nil
}

// optionalString reads a string parameter, returning fallback when absent.
func optionalString(params map[string]any, key, fallback string) (string, error) {
	raw, ok := params[key]
	if !ok {
		return fallback, nil
	}

	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%w: %q wants a string", ErrParamType, key)
	}

	return s, nil
}

// optionalInt reads an integer parameter, returning fallback when absent.
func optionalInt(params map[string]any, key string, fallback int) (int, error) {
	raw, ok := params[key]
	if !ok {
		return fallback, nil
	}

	switch v := raw.(type) {
	case int:
		return v, nil
	case int64:
		return int(v), nil
	case float64:
		return int(v), nil
	default:
		return 0, fmt.Errorf("%w: %q wants an integer", ErrParamType, key)
	}
}

// rejectUnknown refuses a parameter no validator reads, so a typo in a
// routine is a load error rather than a setting that silently does nothing.
func rejectUnknown(params map[string]any, allowed ...string) error {
	known := make(map[string]struct{}, len(allowed))
	for _, a := range allowed {
		known[a] = struct{}{}
	}

	for _, key := range sortedKeys(params) {
		if _, ok := known[key]; !ok {
			return fmt.Errorf("%w %q", ErrUnknownParam, key)
		}
	}

	return nil
}
