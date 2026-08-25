// Package xmlcall reads the `<parameter=key>value</parameter>` dialect that
// Qwen-family models emit when a provider's tool-call parser does not claim
// their output. It arrives in two places: whole calls rendered into the
// message body, and, when the provider claims the call but not its fields,
// the tags folded into the JSON arguments themselves.
package xmlcall

import (
	"encoding/json"
	"strings"
)

// Parameters reads the `<parameter=key>value</parameter>` pairs of one
// rendered call into JSON arguments. Values arrive as text, so a value that
// reads as a number or a boolean is sent as one: a schema asking for an
// integer rejects the string form, and the model meant the number.
func Parameters(block string) json.RawMessage {
	args := map[string]any{}
	rest := block

	for {
		i := strings.Index(rest, "<parameter=")
		if i < 0 {
			break
		}

		rest = rest[i+len("<parameter="):]

		key, body, ok := strings.Cut(rest, ">")
		if !ok {
			break
		}

		value, tail, closed := strings.Cut(body, "</parameter>")
		if !closed {
			value, tail = body, ""
		}

		rest = tail
		args[strings.TrimSpace(key)] = literalOf(strings.Trim(value, "\n"))
	}

	out, err := json.Marshal(args)
	if err != nil {
		return json.RawMessage(`{}`)
	}

	return out
}

func literalOf(value string) any {
	trimmed := strings.TrimSpace(value)

	switch trimmed {
	case "true":
		return true
	case "false":
		return false
	}

	var number float64
	if err := json.Unmarshal([]byte(trimmed), &number); err == nil && trimmed != "" {
		return number
	}

	return value
}
