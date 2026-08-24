package tools

import (
	"strings"
)

// splitImports separates a leading import block from the declaration that
// follows it, returning the import lines and the remaining source.
//
// A caller writing a new test sends the import it needs alongside the
// function, which is what the source looks like in a file. Appending that
// whole thing puts a second import block after existing declarations, which
// Go rejects with "imports must appear before other declarations": measured
// on the `e2` replay task, that is exactly what one run produced.
//
//nolint:nonamedreturns // two string-ish results need naming to be readable
func splitImports(source string) (imports []string, declaration string) {
	trimmed := strings.TrimLeft(source, "\n \t")
	if !strings.HasPrefix(trimmed, "import") {
		return nil, source
	}

	rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "import"))

	if !strings.HasPrefix(rest, "(") {
		line, tail, _ := strings.Cut(rest, "\n")

		return []string{strings.TrimSpace(line)}, strings.TrimLeft(tail, "\n")
	}

	body, tail, found := strings.Cut(rest[1:], ")")
	if !found {
		return nil, source
	}

	var paths []string

	for _, line := range strings.Split(body, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			paths = append(paths, line)
		}
	}

	return paths, strings.TrimLeft(tail, "\n")
}

// mergeImports adds every path the file does not already import to its
// import block, and reports the new source. A file with no import block
// gets one after its package clause.
func mergeImports(source string, paths []string) string {
	missing := make([]string, 0, len(paths))

	for _, p := range paths {
		if !strings.Contains(source, p) {
			missing = append(missing, p)
		}
	}

	if len(missing) == 0 {
		return source
	}

	lines := strings.Split(source, "\n")

	if at := importBlockEnd(lines); at >= 0 {
		merged := append([]string{}, lines[:at]...)
		for _, p := range missing {
			merged = append(merged, "\t"+p)
		}

		return strings.Join(append(merged, lines[at:]...), "\n")
	}

	return openImportBlock(lines, missing)
}

// importBlockEnd is the index of the line closing an existing import block,
// or -1 when the file has none.
func importBlockEnd(lines []string) int {
	open := false

	for i, line := range lines {
		switch {
		case strings.HasPrefix(line, "import ("):
			open = true
		case open && strings.TrimSpace(line) == ")":
			return i
		}
	}

	return -1
}

func openImportBlock(lines, paths []string) string {
	for i, line := range lines {
		if !strings.HasPrefix(line, "package ") {
			continue
		}

		const chrome = 3

		block := make([]string, 0, len(paths)+chrome)
		block = append(block, "", "import (")

		for _, p := range paths {
			block = append(block, "\t"+p)
		}

		block = append(block, ")")

		out := append([]string{}, lines[:i+1]...)
		out = append(out, block...)

		return strings.Join(append(out, lines[i+1:]...), "\n")
	}

	return strings.Join(lines, "\n")
}
