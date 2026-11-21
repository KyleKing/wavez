package hook

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// object is a JSON object that remembers the order of its keys, so that a
// settings file a person arranged by hand comes back arranged the same way.
type object struct {
	values map[string]any
	keys   []string
}

func newObject() *object {
	return &object{values: map[string]any{}}
}

func (o *object) get(key string) (any, bool) {
	value, ok := o.values[key]

	return value, ok
}

// set replaces the value of key in place, or appends key when it is new.
func (o *object) set(key string, value any) {
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}

	o.values[key] = value
}

func (o *object) remove(key string) {
	if _, ok := o.values[key]; !ok {
		return
	}

	delete(o.values, key)

	kept := o.keys[:0]
	for _, existing := range o.keys {
		if existing != key {
			kept = append(kept, existing)
		}
	}

	o.keys = kept
}

var (
	errTrailingJSON = errors.New("unexpected content after the JSON value")
	errObjectKey    = errors.New("an object key is not a string")
)

// decodeJSON parses one JSON value. Objects become *object, arrays []any,
// and numbers json.Number, so nothing is rounded on the way back out.
func decodeJSON(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	value, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}

	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errTrailingJSON
	}

	return value, nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	token, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("reading JSON: %w", err)
	}

	delim, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}

	if delim == '[' {
		return decodeArray(dec)
	}

	return decodeObject(dec)
}

func decodeArray(dec *json.Decoder) ([]any, error) {
	items := []any{}

	for dec.More() {
		item, err := decodeValue(dec)
		if err != nil {
			return nil, err
		}

		items = append(items, item)
	}

	_, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("reading JSON: %w", err)
	}

	return items, nil
}

func decodeObject(dec *json.Decoder) (*object, error) {
	obj := newObject()

	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("reading JSON: %w", err)
		}

		key, ok := token.(string)
		if !ok {
			return nil, errObjectKey
		}

		value, err := decodeValue(dec)
		if err != nil {
			return nil, err
		}

		obj.set(key, value)
	}

	_, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("reading JSON: %w", err)
	}

	return obj, nil
}

// encodeJSON renders a decoded value with two-space indentation and a
// trailing newline, the way Claude Code writes its own settings.
func encodeJSON(value any) ([]byte, error) {
	var buf bytes.Buffer

	if err := encodeValue(&buf, value, 0); err != nil {
		return nil, err
	}

	buf.WriteByte('\n')

	return buf.Bytes(), nil
}

func encodeValue(buf *bytes.Buffer, value any, depth int) error {
	switch typed := value.(type) {
	case *object:
		return encodeItems(buf, depth, "{}", len(typed.keys), func(i int) error {
			if err := encodeScalar(buf, typed.keys[i]); err != nil {
				return err
			}

			buf.WriteString(": ")

			return encodeValue(buf, typed.values[typed.keys[i]], depth+1)
		})
	case []any:
		return encodeItems(buf, depth, "[]", len(typed), func(i int) error {
			return encodeValue(buf, typed[i], depth+1)
		})
	default:
		return encodeScalar(buf, value)
	}
}

// encodeItems writes the n members of an object or array, one per line.
func encodeItems(buf *bytes.Buffer, depth int, brackets string, n int, item func(i int) error) error {
	if n == 0 {
		buf.WriteString(brackets)

		return nil
	}

	buf.WriteByte(brackets[0])

	for i := range n {
		if i > 0 {
			buf.WriteByte(',')
		}

		buf.WriteString("\n" + strings.Repeat("  ", depth+1))

		if err := item(i); err != nil {
			return err
		}
	}

	buf.WriteString("\n" + strings.Repeat("  ", depth))
	buf.WriteByte(brackets[1])

	return nil
}

func encodeScalar(buf *bytes.Buffer, value any) error {
	var scratch bytes.Buffer

	enc := json.NewEncoder(&scratch)
	// A hook command holds && and > far more often than it holds HTML.
	enc.SetEscapeHTML(false)

	if err := enc.Encode(value); err != nil {
		return fmt.Errorf("writing JSON: %w", err)
	}

	buf.Write(bytes.TrimRight(scratch.Bytes(), "\n"))

	return nil
}
