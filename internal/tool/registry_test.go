package tool_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/kyleking/wavez/internal/tool"
)

type namedTool struct{ name string }

func (t namedTool) Name() string          { return t.name }
func (namedTool) Description() string     { return "does a thing" }
func (namedTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

func (namedTool) Run(context.Context, json.RawMessage) (tool.Result, error) {
	return tool.Result{Content: "ok"}, nil
}

func TestRegistryOnly(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		keep []string
		want []string
	}{
		{
			name: "keeps the registry's order, not the argument's",
			keep: []string{"write", "read"},
			want: []string{"read", "write"},
		},
		{
			name: "a name the registry does not hold is skipped",
			keep: []string{"read", "modify"},
			want: []string{"read"},
		},
		{name: "no names keeps nothing", keep: nil, want: []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			full := tool.NewRegistry(namedTool{name: "read"}, namedTool{name: "write"}, namedTool{name: "shell"})

			got := full.Only(tt.keep...)
			if !slices.Equal(got.Names(), tt.want) {
				t.Errorf("Names() = %v, want %v", got.Names(), tt.want)
			}

			if len(got.Specs()) != len(tt.want) {
				t.Errorf("Specs() len = %d, want %d", len(got.Specs()), len(tt.want))
			}

			if len(full.Names()) != 3 {
				t.Errorf("Only mutated the source registry: %v", full.Names())
			}
		})
	}
}

// A narrowed registry has to refuse the tools it dropped, not merely stop
// advertising them: a model that names an unadvertised tool would otherwise
// still reach it.
func TestRegistryOnlyRefusesWhatItDropped(t *testing.T) {
	t.Parallel()

	narrowed := tool.NewRegistry(namedTool{name: "read"}, namedTool{name: "write"}).Only("read")

	if _, err := narrowed.Get("read"); err != nil {
		t.Errorf("Get(read) = %v, want the kept tool", err)
	}

	if _, err := narrowed.Get("write"); !errors.Is(err, tool.ErrNotFound) {
		t.Errorf("Get(write) = %v, want ErrNotFound", err)
	}
}
