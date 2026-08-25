package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// decodeInput reads a tool call's arguments, translating the decoder's own
// error into one a model can act on. The decoder names Go types
// ("[]tools.editPair"), which say nothing about the JSON that has to change:
// two logged runs sent `edits` as a string holding the array and got that
// message back, and neither changed the shape on the next attempt.
func decodeInput(raw json.RawMessage, into any) error {
	err := json.Unmarshal(raw, into)
	if err == nil {
		return nil
	}

	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) && typeErr.Field != "" {
		//nolint:err113 // the sentence is the whole value; nothing matches on it
		return fmt.Errorf("%s must be %s, and %s was sent instead",
			typeErr.Field, jsonShape(typeErr.Type), typeErr.Value)
	}

	return err //nolint:wrapcheck // a syntax error already reads as one
}

// jsonShape names a Go type the way the JSON sent for it looks, since that
// is the thing the caller has to change.
func jsonShape(t reflect.Type) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		return "an array of " + jsonShape(t.Elem()) + " items"
	case reflect.Struct, reflect.Map:
		return "an object"
	case reflect.String:
		return "a string"
	case reflect.Bool:
		return "true or false"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return "a number"
	default:
		return strings.ToLower(t.Kind().String())
	}
}
