package tools

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/kyleking/wavez/internal/xmlcall"
)

// decodeInput reads a tool call's arguments, translating the decoder's own
// error into one a model can act on. The decoder names Go types
// ("[]tools.editPair"), which say nothing about the JSON that has to change:
// two logged runs sent `edits` as a string holding the array and got that
// message back, and neither changed the shape on the next attempt.
func decodeInput(raw json.RawMessage, into any) error {
	recovered, err := xmlRecovered(raw)
	if err != nil {
		return err
	}

	if recovered != nil {
		raw = recovered
	}

	err = json.Unmarshal(raw, into)
	for tries := 0; err != nil && tries < maxBoolCoercions; tries++ {
		coerced, ok := coerceQuotedBool(raw, err)
		if !ok {
			break
		}

		raw = coerced
		err = json.Unmarshal(raw, into)
	}

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

// maxBoolCoercions bounds the retry loop at more flags than any tool's input
// carries, so a decoder that keeps failing stops rather than spinning.
const maxBoolCoercions = 8

// coerceQuotedBool rewrites one field a caller quoted a boolean into, where
// the decoder has just said that field wants a bool and got a string.
// Measured on `h3`: a lane told to send replace_all: true sent "true", was
// refused, and never sent the call again. The type error names the field, so
// nothing else in the object is touched and a string field holding the word
// "true" is left alone.
func coerceQuotedBool(raw json.RawMessage, err error) (json.RawMessage, bool) {
	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) || typeErr.Field == "" ||
		typeErr.Type.Kind() != reflect.Bool || typeErr.Value != "string" {
		return nil, false
	}

	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil, false
	}

	var quoted string
	if json.Unmarshal(fields[typeErr.Field], &quoted) != nil {
		return nil, false
	}

	switch strings.ToLower(quoted) {
	case "true":
		fields[typeErr.Field] = json.RawMessage("true")
	case "false":
		fields[typeErr.Field] = json.RawMessage("false")
	default:
		return nil, false
	}

	out, marshalErr := json.Marshal(fields)
	if marshalErr != nil {
		return nil, false
	}

	return out, true
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

// xmlRecovered rebuilds the arguments of a call that arrived in the XML
// tool-call syntax some models emit natively, mangled into a JSON object on
// the way. It returns the recovered arguments, or an error naming the shape
// to resend when nothing survived.
//
// The mangling folds the first tag and its value into one key and drops the
// delimiters, so that pair is lost; every later tag reaches the value
// intact. All four recorded cases are one lost pair beside one intact one,
// and in each the lost pair was an optional field with a default while the
// intact one carried the query. Reading what survived is recovery, not
// repair: a field the tags no longer delimit is dropped rather than guessed,
// and a required field among them still fails the decode below.
//
// Only a key is inspected. A value may hold this text legitimately, since
// an edit to a file that documents the syntax carries it verbatim, while no
// argument is ever named for a tag.
func xmlRecovered(raw json.RawMessage) (json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		// Not an object at all, which the decoder reports in its own words.
		return nil, nil //nolint:nilerr // see comment: a non-object is not this check's finding
	}

	tagged := make([]string, 0, len(object))
	fields := map[string]json.RawMessage{}

	for key, value := range object {
		if xmlTagged(key) {
			tagged = append(tagged, key)

			continue
		}

		fields[key] = value
	}

	if len(tagged) == 0 {
		return nil, nil
	}

	// Sorted so a call carrying more than one mangled key rebuilds the same
	// block every time rather than following map order.
	sort.Strings(tagged)

	var block strings.Builder

	for _, key := range tagged {
		block.WriteString(key)

		var text string
		if err := json.Unmarshal(object[key], &text); err == nil {
			block.WriteString(text)
		}
	}

	var salvaged map[string]json.RawMessage
	if err := json.Unmarshal(xmlcall.Parameters(block.String()), &salvaged); err != nil ||
		len(salvaged) == 0 {
		//nolint:err113 // the sentence is the whole value; nothing matches on it
		return nil, errors.New("the arguments arrived as XML parameter tags rather than as " +
			"JSON, so no field could be read and nothing ran. Send one JSON object whose keys " +
			"are the field names this tool declares, with no tags around them")
	}

	for key, value := range salvaged {
		fields[key] = value
	}

	out, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("rebuilding the arguments: %w", err)
	}

	return out, nil
}

func xmlTagged(key string) bool {
	return strings.Contains(key, "<parameter") || strings.Contains(key, "</parameter") ||
		strings.Contains(key, "parameter=")
}
