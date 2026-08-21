// Package tools implements the v0.1 tool set the chat loop hands to a model:
// list, read, str_replace, write, shell, search, and question. Each tool is
// constructed with explicit dependencies (a project root, a gate, a store)
// and holds no package-level state.
package tools

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrPathOutsideRoot reports a tool argument naming a path outside the
// project root.
var ErrPathOutsideRoot = errors.New("path is outside the project root")

// ErrPathMissing reports a call that left path out. It is its own error
// because reporting it as a containment failure describes the wrong
// problem: a run that batched edits without naming the file was told its
// path was outside the project root and never sent one.
var ErrPathMissing = errors.New("path is required and was not set")

// resolvePath resolves path against root and refuses one that lexically
// escapes it, whether given as an absolute path or a relative one that
// walks out with "..".
func resolvePath(root, path string) (string, error) {
	if path == "" {
		return "", ErrPathMissing
	}

	var abs string
	if filepath.IsAbs(path) {
		abs = filepath.Clean(path)
	} else {
		abs = filepath.Clean(filepath.Join(root, path))
	}

	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %s", ErrPathOutsideRoot, path)
	}

	return abs, nil
}

// lineNumbered reports text that looks like it was copied out of a read
// result with the line numbers still attached: every non-blank line opens
// with digits and a tab. Read numbers what it returns, so an anchor or a
// file body in that shape is the number prefix leaking back in, which no
// file holds and which would either fail to match or be written verbatim.
func lineNumbered(text string) bool {
	var checked int
	for line := range strings.SplitSeq(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		tab := strings.IndexByte(line, '\t')
		if tab <= 0 || !allDigits(line[:tab]) {
			return false
		}
		checked++
	}

	return checked > 0
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}

// repeatedStringField returns every top-level string value a JSON object
// gives the named key. Decoding keeps only the last of a repeated key, which
// loses what a model meant when it batches by repeating one.
func repeatedStringField(input json.RawMessage, key string) []string {
	dec := json.NewDecoder(bytes.NewReader(input))

	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return nil
	}

	var out []string
	for dec.More() {
		name, err := dec.Token()
		if err != nil {
			return out
		}

		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return out
		}

		if name != key {
			continue
		}

		var s string
		if err := json.Unmarshal(value, &s); err == nil {
			out = append(out, s)
		}
	}

	return out
}
