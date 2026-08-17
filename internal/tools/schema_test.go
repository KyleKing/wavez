package tools_test

import (
	"encoding/json"
	"testing"

	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/tool"
	"github.com/kyleking/wavez/internal/tools"
)

type jsonSchema struct {
	Properties map[string]json.RawMessage `json:"properties"`
	Type       string                     `json:"type"`
	Required   []string                   `json:"required"`
}

type property struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

func allTools(t *testing.T) []tool.Tool {
	t.Helper()

	dir := t.TempDir()

	return []tool.Tool{
		tools.NewRead(dir, nil),
		tools.NewStrReplace(dir, nil),
		tools.NewWrite(dir, nil),
		tools.NewShell(dir, dir, "thread-1", permission.AllowAll()),
		tools.NewSearch(openTestIndex(t, nil)),
		tools.NewQuestion(fakeAsker{}),
	}
}

func TestSchemas_AreValidJSONSchemaObjects(t *testing.T) {
	t.Parallel()

	for _, tl := range allTools(t) {
		t.Run(tl.Name(), func(t *testing.T) {
			t.Parallel()
			validateSchema(t, tl)
		})
	}
}

func validateSchema(t *testing.T, tl tool.Tool) {
	t.Helper()

	if tl.Name() == "" {
		t.Errorf("Name() is empty")
	}

	if tl.Description() == "" {
		t.Errorf("Description() is empty")
	}

	var schema jsonSchema
	if err := json.Unmarshal(tl.Schema(), &schema); err != nil {
		t.Fatalf("Schema() is not valid JSON: %v", err)
	}

	if schema.Type != "object" {
		t.Errorf("type = %q, want %q", schema.Type, "object")
	}

	if len(schema.Properties) == 0 {
		t.Fatalf("properties is empty")
	}

	if len(schema.Required) == 0 {
		t.Fatalf("required is empty")
	}

	for _, name := range schema.Required {
		if _, ok := schema.Properties[name]; !ok {
			t.Errorf("required %q is not a key in properties", name)
		}
	}

	for name, raw := range schema.Properties {
		validateProperty(t, name, raw)
	}
}

func validateProperty(t *testing.T, name string, raw json.RawMessage) {
	t.Helper()

	var prop property
	if err := json.Unmarshal(raw, &prop); err != nil {
		t.Errorf("property %q is not a JSON object: %v", name, err)

		return
	}

	if prop.Type == "" {
		t.Errorf("property %q has no type", name)
	}

	if prop.Description == "" {
		t.Errorf("property %q has no description", name)
	}
}
