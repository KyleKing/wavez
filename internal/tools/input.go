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
	if err := xmlCallError(raw); err != nil {
		return err
	}

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

// xmlCallError reports a call whose arguments arrived in the XML tool-call
// syntax some models emit natively, mangled into a JSON object on the way.
// The pairs cannot be recovered, because the mangling folds a tag and its
// value into one key, so the call is refused with the shape to resend
// rather than repaired into a plausible wrong one.
//
// Only a key is inspected. A value may hold this text legitimately, since
// an edit to a file that documents the syntax carries it verbatim, while no
// argument is ever named for a tag.
func xmlCallError(raw json.RawMessage) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		// Not an object at all, which the decoder below reports in its own
		// words.
		return nil //nolint:nilerr // see comment: a non-object is not this check's finding
	}

	for key := range object {
		if !strings.Contains(key, "<parameter") && !strings.Contains(key, "</parameter") &&
			!strings.Contains(key, "parameter=") {
			continue
		}

		//nolint:err113 // the sentence is the whole value; nothing matches on it
		return errors.New("the arguments arrived as XML parameter tags rather than as JSON, " +
			"so no field could be read and nothing ran. Send one JSON object whose keys are " +
			"the field names this tool declares, with no tags around them")
	}

	return nil
}
