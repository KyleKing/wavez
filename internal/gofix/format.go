package gofix

import (
	"bytes"
	"strings"

	"golang.org/x/tools/imports"
)

const gofmtTabWidth = 8

var formatOptions = &imports.Options{Comments: true, TabIndent: true, TabWidth: gofmtTabWidth, FormatOnly: false}

// Format applies gofmt and goimports in process, reporting whether the
// source changed. Both are libraries, so a released binary formats the way a
// developer's checkout does. Source that does not parse comes back unchanged,
// because a parse error is the build gate's report to make.
//
// The edit tools run this before they return, so the text a caller is shown
// is the text the format gate would otherwise rewrite behind it a moment
// later. An anchor taken from a stale view is the largest single cause of a
// wasted turn here.
func Format(path string, src []byte) ([]byte, bool) {
	if !strings.HasSuffix(path, ".go") {
		return src, false
	}

	out, err := imports.Process(path, src, formatOptions)
	if err != nil {
		return src, false
	}

	if bytes.Equal(out, src) {
		return src, false
	}

	return out, true
}
