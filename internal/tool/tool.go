// Package tool defines the tool surface the agent loop exposes to a model.
package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrNotFound reports a tool call naming a tool that is not registered.
var ErrNotFound = errors.New("tool not found")

// LineRange is an inclusive 1-indexed line span within a file.
type LineRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Change describes one file a tool touched. Gates trigger on these rather than
// on the model asking for a test run.
type Change struct {
	Path    string      `json:"path"`
	Ranges  []LineRange `json:"ranges,omitempty"`
	Added   int         `json:"added"`
	Removed int         `json:"removed"`
}

// Result is what a tool returns. Content is the only part the model sees, so it
// is already trimmed by the tool's own rules.
type Result struct {
	Content string   `json:"content"`
	Changes []Change `json:"changes,omitempty"`
	IsError bool     `json:"is_error,omitempty"`
}

// Errorf builds a Result carrying a failure the model is expected to correct.
func Errorf(format string, args ...any) Result {
	return Result{Content: fmt.Sprintf(format, args...), IsError: true}
}

// Spec advertises one tool to a provider.
type Spec struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

// Tool is one deterministic operation the model may invoke. Run must be safe to
// call concurrently with other tools and must not retain input after returning.
type Tool interface {
	Name() string
	Description() string
	Schema() json.RawMessage
	Run(ctx context.Context, input json.RawMessage) (Result, error)
}

// Registry resolves tool calls by name and advertises the set to a provider.
type Registry struct {
	byName map[string]Tool
	order  []string
}

// NewRegistry builds a registry over tools, panicking on a duplicate name
// because the tool set is fixed at build time.
func NewRegistry(tools ...Tool) *Registry {
	r := &Registry{byName: make(map[string]Tool, len(tools))}
	for _, t := range tools {
		if _, dup := r.byName[t.Name()]; dup {
			panic("duplicate tool name: " + t.Name())
		}
		r.byName[t.Name()] = t
		r.order = append(r.order, t.Name())
	}

	return r
}

// Get returns the named tool, or ErrNotFound.
//
//nolint:ireturn // the registry exists to hand back the Tool interface
func (r *Registry) Get(name string) (Tool, error) {
	t, ok := r.byName[name]
	if !ok {
		return nil, ErrNotFound
	}

	return t, nil
}

// Specs advertises every registered tool to a provider, in registration order.
// The order is stable so the prompt prefix stays cacheable across turns.
func (r *Registry) Specs() []Spec {
	out := make([]Spec, 0, len(r.order))
	for _, name := range r.order {
		t := r.byName[name]
		out = append(out, Spec{Name: t.Name(), Description: t.Description(), Schema: t.Schema()})
	}

	return out
}

// Only returns a registry holding just the named tools, keeping this
// registry's order. Names this registry does not hold are skipped rather
// than reported, so a caller's allowlist may safely name a tool that has
// not shipped yet.
//
// It is an allowlist rather than a denylist because the failure modes are
// not symmetric: a tool added later is excluded until someone says
// otherwise, where a denylist would admit it silently into every narrowed
// registry that exists.
func (r *Registry) Only(names ...string) *Registry {
	out := &Registry{byName: make(map[string]Tool, len(names))}

	wanted := make(map[string]bool, len(names))
	for _, n := range names {
		wanted[n] = true
	}

	for _, name := range r.order {
		if !wanted[name] {
			continue
		}

		out.byName[name] = r.byName[name]
		out.order = append(out.order, name)
	}

	return out
}

// Names lists registered tools in registration order.
func (r *Registry) Names() []string {
	out := make([]string, len(r.order))
	copy(out, r.order)

	return out
}
