package tools

import "encoding/json"

// JSON Schema type and property names repeated across every tool's schema.
const (
	schemaTypeBoolean = "boolean"
	schemaTypeString  = "string"
	schemaTypeInteger = "integer"
	schemaTypeArray   = "array"
	schemaTypeObject  = "object"
	propPath          = "path"
	propQuestion      = "question"
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

// oneOfSchemaDoc is a schema whose input is exactly one of several object
// shapes.
type oneOfSchemaDoc struct {
	OneOf []jsonSchemaDoc `json:"oneOf"` //nolint:tagliatelle // JSON Schema spells it oneOf
}

// buildSchema renders properties and required into a JSON Schema object.
// It panics on a marshal failure, which only a build-time bug in a
// package-level schema literal could cause.
func buildSchema(properties map[string]schemaProperty, required ...string) json.RawMessage {
	return marshalSchema(jsonSchemaDoc{Type: "object", Properties: properties, Required: required})
}

// buildOneOf renders alternative input shapes, each with its own required
// list, as a top-level oneOf.
//
// The nesting is load-bearing rather than stylistic. A local turn decodes
// under a grammar compiled from this schema, so every field a branch leaves
// out of required is an exit the model can take mid-call. A top-level oneOf
// compiles to alternative productions, while an anyOf placed beside
// properties is ignored, so alternatives have to be whole objects to bind.
func buildOneOf(branches ...jsonSchemaDoc) json.RawMessage {
	return marshalSchema(oneOfSchemaDoc{OneOf: branches})
}

// branch is one alternative input shape, with every property it accepts
// required.
func branch(properties map[string]schemaProperty, required ...string) jsonSchemaDoc {
	return jsonSchemaDoc{Type: "object", Properties: properties, Required: required}
}

func marshalSchema(doc any) json.RawMessage {
	data, err := json.Marshal(doc)
	if err != nil {
		panic("tools: invalid schema: " + err.Error())
	}

	return data
}
