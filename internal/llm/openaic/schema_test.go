package openaic_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/llm/openaic"
)

// A composition reaches a provider the same way wherever it sits, so a
// dialect that cannot read one at the top of a schema cannot read one inside
// a property either. The merge is what keeps that safe: a node stating
// `properties` beside a composition means both, and collapsing to the branch
// alone would send a tool with no arguments.
func TestNormalizeSchema_ReachesEveryDepthAndKeepsTheSiblings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		schema  string
		absent  []string
		present []string
	}{
		{
			name:    "a nested anyOf is collapsed",
			schema:  `{"type":"object","properties":{"target":{"anyOf":[{"type":"string"},{"type":"integer"}]}}}`,
			absent:  []string{`"anyOf"`, `"integer"`},
			present: []string{`"target"`, `"string"`},
		},
		{
			name:    "a composition beside properties keeps them",
			schema:  `{"type":"object","properties":{"path":{"type":"string"}},"anyOf":[{"required":["path"]}]}`,
			absent:  []string{`"anyOf"`},
			present: []string{`"path"`, `"required"`},
		},
		{
			name:    "an empty branch list is dropped rather than guessed at",
			schema:  `{"type":"object","properties":{"path":{"type":"string"}},"oneOf":[]}`,
			absent:  []string{`"oneOf"`},
			present: []string{`"path"`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := string(openaic.NormalizeSchema(json.RawMessage(tc.schema), openaic.DialectZAI))

			for _, want := range tc.present {
				if !strings.Contains(got, want) {
					t.Errorf("normalized schema dropped %s: %s", want, got)
				}
			}

			for _, unwanted := range tc.absent {
				if strings.Contains(got, unwanted) {
					t.Errorf("normalized schema still carries %s: %s", unwanted, got)
				}
			}

			if same := string(openaic.NormalizeSchema(json.RawMessage(tc.schema),
				openaic.DialectOpenRouter)); same != tc.schema {
				t.Errorf("a dialect rejecting nothing rewrote the schema:\n%s", same)
			}
		})
	}
}
