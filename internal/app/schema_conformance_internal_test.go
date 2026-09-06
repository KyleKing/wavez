package app

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/llm/openaic"
)

// Every dialect this project dials, so a schema is checked against the whole
// surface rather than against the one that broke last.
var dialects = []openaic.Dialect{openaic.DialectLlamaCpp, openaic.DialectOpenRouter, openaic.DialectZAI}

// A dialect declares the JSON Schema constructs it rejects, and the risk a
// declared set carries that a hand-coded special case did not is
// over-stripping: normalizing away the thing that made a tool callable, on a
// provider nobody runs the test suite against. This asserts both halves over
// the tools a real project is given. A tool the model cannot state arguments
// for is a tool it answers with `{}`, which is how the z.ai case was found in
// the first place.
func TestEveryToolSchemaSurvivesEveryDialect(t *testing.T) {
	t.Parallel()

	registry := buildRegistry(registryDeps{root: t.TempDir(), asker: nopAsker{}, web: true})

	for _, spec := range registry.Specs() {
		for _, d := range dialects {
			t.Run(spec.Name+"/"+string(d), func(t *testing.T) {
				t.Parallel()

				assertNormalized(t, openaic.NormalizeSchema(spec.Schema, d), d)
			})
		}
	}
}

func assertNormalized(t *testing.T, normalized json.RawMessage, d openaic.Dialect) {
	t.Helper()

	var node map[string]any
	if err := json.Unmarshal(normalized, &node); err != nil {
		t.Fatalf("the normalized schema does not parse: %v\n%s", err, normalized)
	}

	for _, keyword := range keywordsIn(node) {
		if slices.Contains(d.RejectedKeywords(), keyword) {
			t.Errorf("the normalized schema still carries %q, which %s rejects:\n%s", keyword, d, normalized)
		}
	}

	properties, ok := node["properties"].(map[string]any)
	if !ok || len(properties) == 0 {
		t.Fatalf("normalizing left no properties, so nothing can state an argument:\n%s", normalized)
	}

	for _, req := range requiredIn(node) {
		if _, ok := properties[req]; !ok {
			t.Errorf("required names %q, which the normalized properties do not declare:\n%s", req, normalized)
		}
	}
}

// keywordsIn is every object key anywhere in the schema, which is what a
// rejection has to be checked against: a construct nested inside a property
// reaches the provider exactly as a top-level one does.
func keywordsIn(node any) []string {
	var out []string

	switch n := node.(type) {
	case map[string]any:
		for key, child := range n {
			out = append(out, key)
			out = append(out, keywordsIn(child)...)
		}
	case []any:
		for _, child := range n {
			out = append(out, keywordsIn(child)...)
		}
	}

	return out
}

func requiredIn(node map[string]any) []string {
	raw, ok := node["required"].([]any)
	if !ok {
		return nil
	}

	out := make([]string, 0, len(raw))

	for _, v := range raw {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}

	return out
}
