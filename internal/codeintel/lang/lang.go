// Package lang extracts symbols from source text through tree-sitter.
// Adding a language means implementing Language and adding one line to
// NewDefaultRegistry; nothing else in codeintel changes.
package lang

import (
	"errors"
	"fmt"
	"path/filepath"
)

// Kind values Language implementations use for Symbol.Kind, kept as
// constants so "func", "type", and friends are not repeated string
// literals across languages.
const (
	KindFunc   = "func"
	KindMethod = "method"
	KindType   = "type"
	KindClass  = "class"
)

// Symbol is one extracted declaration: a function, method, type, or class.
// Byte and line offsets are 0-indexed and end-exclusive on bytes,
// end-inclusive on lines, matching tree-sitter's own node ranges.
type Symbol struct {
	Kind      string
	Name      string
	Signature string
	Doc       string
	StartByte uint
	EndByte   uint
	StartLine int
	EndLine   int
}

// Language extracts Symbols from one language's source text.
type Language interface {
	// Extensions lists the file extensions this language claims, each
	// including the leading dot.
	Extensions() []string
	// Extract parses src and returns its top-level and nested declarations.
	Extract(src []byte) ([]Symbol, error)
}

// Registry resolves a file path to the Language that should index it.
type Registry struct {
	byExt map[string]Language
}

// NewDefaultRegistry builds the registry for every language this build
// ships: Go and Python.
func NewDefaultRegistry() *Registry {
	r := &Registry{byExt: make(map[string]Language)}
	r.register(newGo())
	r.register(newPython())

	return r
}

func (r *Registry) register(l Language) {
	for _, ext := range l.Extensions() {
		r.byExt[ext] = l
	}
}

// Claims reports whether some registered Language handles path's
// extension.
func (r *Registry) Claims(path string) bool {
	_, ok := r.byExt[filepath.Ext(path)]

	return ok
}

// ErrUnclaimedExtension reports Extract called for a path no registered
// Language handles; callers are expected to check Claims first.
var ErrUnclaimedExtension = errors.New("no language registered for this extension")

// Extract parses src using the Language registered for path's extension.
func (r *Registry) Extract(path string, src []byte) ([]Symbol, error) {
	l, ok := r.byExt[filepath.Ext(path)]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnclaimedExtension, path)
	}
	symbols, err := l.Extract(src)
	if err != nil {
		return nil, fmt.Errorf("extracting %s: %w", path, err)
	}

	return symbols, nil
}
