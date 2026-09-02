// Package glob matches a path against the patterns a user writes in this
// project's config: the globs that scope a gate to a directory, a routine to
// a file type, and a listing to part of a tree.
//
// It exists because the standard library has no `**`. path.Match reads it as
// a single `*`, which matches within one path segment, so `apps/api/**/*.py`
// matched a file exactly two levels down and nothing deeper, and a check
// scoped to a subtree in a repository holding several silently ran on almost
// none of it. Nothing said so: a pattern that matches nothing and a change
// set holding nothing it names are the same abstention.
package glob

import (
	"fmt"
	"path"
	"regexp"
	"strings"
	"sync"
)

// Match reports whether rel, a slash-separated relative path, matches
// pattern.
//
// A pattern holding no slash is matched against the base name, so `*.py`
// names every Python file at any depth. One holding a slash is matched
// against the whole path, where `*` and `?` stay inside a segment and `**`
// spans any number of them, including none: `apps/**/main.py` matches
// `apps/main.py` and `apps/api/v2/main.py` alike.
func Match(pattern, rel string) bool {
	if pattern == "" {
		return true
	}

	if !strings.Contains(pattern, "/") {
		return matchSegment(pattern, path.Base(rel))
	}

	if !strings.Contains(pattern, "**") {
		return matchSegment(pattern, rel)
	}

	re, err := compiled(pattern)
	if err != nil {
		return false
	}

	return re.MatchString(rel)
}

func matchSegment(pattern, subject string) bool {
	ok, err := path.Match(pattern, subject)

	return err == nil && ok
}

var cache sync.Map

// compiled translates a `**` pattern to a regexp once and remembers it, since
// the same handful of patterns are matched against every change of every run.
func compiled(pattern string) (*regexp.Regexp, error) {
	if got, ok := cache.Load(pattern); ok {
		re, isRegexp := got.(*regexp.Regexp)
		if isRegexp {
			return re, nil
		}
	}

	re, err := regexp.Compile(translate(pattern))
	if err != nil {
		return nil, fmt.Errorf("compiling the pattern %q: %w", pattern, err)
	}

	cache.Store(pattern, re)

	return re, nil
}

// translate builds the regexp for a pattern. `**/` spans any number of
// segments including none, a bare `**` spans the rest of the path, `*` and
// `?` stay inside one segment, and everything else is literal.
func translate(pattern string) string {
	var b strings.Builder

	b.WriteString("^")

	for i := 0; i < len(pattern); i++ {
		switch {
		case strings.HasPrefix(pattern[i:], "**/"):
			b.WriteString("(?:[^/]*/)*")
			i += 2
		case strings.HasPrefix(pattern[i:], "**"):
			b.WriteString(".*")
			i++
		case pattern[i] == '*':
			b.WriteString("[^/]*")
		case pattern[i] == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(pattern[i : i+1]))
		}
	}

	b.WriteString("$")

	return b.String()
}
