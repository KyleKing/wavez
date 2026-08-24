package gofix

import (
	"go/parser"
	"go/token"
	"strings"
)

// BrokeSyntax reports the parse error an edit introduced. Source that
// already failed to parse, or still parses, has nothing to report.
func BrokeSyntax(path string, before, after []byte) (string, bool) {
	if !strings.HasSuffix(path, ".go") {
		return "", false
	}

	if !parses(path, before) {
		return "", false
	}

	if _, err := parser.ParseFile(token.NewFileSet(), path, after, parser.SkipObjectResolution); err != nil {
		return err.Error(), true
	}

	return "", false
}

func parses(path string, src []byte) bool {
	_, err := parser.ParseFile(token.NewFileSet(), path, src, parser.SkipObjectResolution)

	return err == nil
}
