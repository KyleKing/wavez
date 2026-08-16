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
	Added   int         `json:"added"`
	Removed int         `json:"removed"`
	Ranges  []LineRange `json:"ranges,omitempty"`
}

// Result is what a tool returns. Content is the only part the model sees, so it
// is already trimmed by the tool's own rules.
type Result struct {
	Content string   `json:"content"`
	IsError bool     `json:"is_error,omitempty"`
	Changes []Change `json:"changes,omitempty"`
}

// Errorf builds a Result carrying a failure the model is expected to correct.
func Errorf(format string, args ...any) Result {
	return Result{Content: fmt.Sprintf(format, args...), IsError: true}
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
func (r *Registry) Get(name string) (Tool, error) {
	t, ok := r.byName[name]
	if !ok {
		return nil, ErrNotFound
	}
	return t, nil
}

// Names lists registered tools in registration order.
func (r *Registry) Names() []string {
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}
