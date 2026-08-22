package tools

import "encoding/json"

// JSON Schema type and property names repeated across every tool's schema.
const (
	schemaTypeString  = "string"
	schemaTypeInteger = "integer"
	schemaTypeArray   = "array"
	schemaTypeObject  = "object"
	propPath          = "path"
	propSymbol        = "symbol"
)

// schemaProperty is one property of a tool's JSON Schema input. Items
// describes the element type of an array property and is nil for every
// other type.
type schemaProperty struct {
	Items       *schemaItems `json:"items,omitempty"`
	Type        string       `json:"type"`
	Description string       `json:"description"`
	Enum        []string     `json:"enum,omitempty"`
}

// schemaItems is the object an array property's elements are.
type schemaItems struct {
	Properties map[string]schemaProperty `json:"properties,omitempty"`
	Type       string                    `json:"type"`
	Required   []string                  `json:"required,omitempty"`
}

// jsonSchemaDoc is the JSON Schema object a tool.Tool's Schema method
// returns: an object type with named, described properties and a required
// list.
type jsonSchemaDoc struct {
	Type       string                    `json:"type"`
	Properties map[string]schemaProperty `json:"properties"`
	Required   []string                  `json:"required"`
}

// buildSchema renders properties and required into a JSON Schema object.
// It panics on a marshal failure, which only a build-time bug in a
// package-level schema literal could cause.
func buildSchema(properties map[string]schemaProperty, required ...string) json.RawMessage {
	doc := jsonSchemaDoc{Type: "object", Properties: properties, Required: required}

	data, err := json.Marshal(doc)
	if err != nil {
		panic("tools: invalid schema: " + err.Error())
	}

	return data
}
