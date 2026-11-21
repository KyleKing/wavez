package config

import (
	"fmt"
	"regexp"
	"strings"
)

// Glob is a compiled path pattern. `*` and `?` stay within one path segment,
// `**` crosses segments, and `[...]` is a character class. A pattern with no
// slash matches a file of that name at any depth, the way a gitignore line
// does, so `*.py` and `**/*.py` select the same files.
type Glob struct {
	re      *regexp.Regexp
	pattern string
}

// ParseGlob compiles pattern, failing on an empty one or an unclosed class.
func ParseGlob(pattern string) (Glob, error) {
	if pattern == "" {
		return Glob{}, &GlobError{Pattern: pattern, Problem: problemEmpty}
	}

	expr := pattern
	if !strings.Contains(pattern, "/") {
		expr = "**/" + pattern
	}

	re, err := regexp.Compile("^" + translate(strings.TrimPrefix(expr, "./")) + "$")
	if err != nil {
		return Glob{}, &GlobError{Pattern: pattern, Problem: "is not a valid pattern"}
	}

	return Glob{re: re, pattern: pattern}, nil
}

// Match reports whether the slash-separated relative path matches. The zero
// Glob matches nothing.
func (g Glob) Match(path string) bool {
	return g.re != nil && g.re.MatchString(strings.TrimPrefix(path, "./"))
}

// String returns the pattern as written.
func (g Glob) String() string { return g.pattern }

// GlobError is a pattern ParseGlob refused.
type GlobError struct {
	Pattern string
	Problem string
}

func (e *GlobError) Error() string {
	return fmt.Sprintf("pattern %q %s", e.Pattern, e.Problem)
}

// translate rewrites a glob as a regular expression body. A class is passed
// through for regexp to validate, with a leading `!` read as negation.
func translate(pattern string) string {
	var b strings.Builder

	for i := 0; i < len(pattern); i++ {
		switch c := pattern[i]; {
		case strings.HasPrefix(pattern[i:], "**/"):
			b.WriteString(`(?:[^/]+/)*`)

			i += len("**/") - 1
		case strings.HasPrefix(pattern[i:], "**"):
			b.WriteString(`.*`)

			i++
		case c == '*':
			b.WriteString(`[^/]*`)
		case c == '?':
			b.WriteString(`[^/]`)
		case c == '[':
			end := strings.IndexByte(pattern[i:], ']')
			if end < 0 {
				b.WriteByte(c)

				continue
			}

			b.WriteString(strings.Replace(pattern[i:i+end+1], "[!", "[^", 1))

			i += end
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}

	return b.String()
}
