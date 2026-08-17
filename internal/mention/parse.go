package mention

import (
	"strings"
	"unicode"
)

// scan returns the mention references in prompt, in first-appearance order
// with duplicates dropped.
//
// The grammar is deliberately narrow, because an `@` in a prompt is far more
// often an email address, a decorator, or a struct tag than a mention:
//
//   - a mention starts at `@` preceded by the start of a line, a space, a
//     quote, or an opening bracket, which rules out `kyle@example.com`
//   - its reference is a run of letters, digits, `_`, `.`, `/`, and `-`,
//     with trailing `.` and `-` dropped so sentence punctuation is not part
//     of the path
//   - a reference followed immediately by `(` is a call or a decorator
//     (`@pytest.mark.parametrize(...)`), never a mention
//   - fenced blocks and inline code spans are skipped whole, which is what
//     keeps pasted source (struct tags, decorators, annotations) out
func scan(prompt string) []string {
	var (
		refs    []string
		seen    = map[string]bool{}
		inFence bool
	)

	for _, line := range strings.Split(prompt, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence

			continue
		}
		if inFence {
			continue
		}

		for _, segment := range codeFreeSegments(line) {
			for _, ref := range scanSegment(segment) {
				if seen[ref] {
					continue
				}
				seen[ref] = true
				refs = append(refs, ref)
			}
		}
	}

	return refs
}

// codeFreeSegments drops backtick-delimited spans from line. An unbalanced
// backtick makes the rest of the line code, which errs toward ignoring an
// `@` rather than expanding one the user meant literally.
func codeFreeSegments(line string) []string {
	parts := strings.Split(line, "`")
	segments := make([]string, 0, len(parts)/2+1)
	for i := 0; i < len(parts); i += 2 {
		segments = append(segments, parts[i])
	}

	return segments
}

func scanSegment(segment string) []string {
	var refs []string

	runes := []rune(segment)
	for i := 0; i < len(runes); i++ {
		if runes[i] != '@' {
			continue
		}
		if i > 0 && !isMentionBoundary(runes[i-1]) {
			continue
		}

		end := i + 1
		for end < len(runes) && isRefRune(runes[end]) {
			end++
		}

		ref := strings.TrimRight(string(runes[i+1:end]), ".-")
		i = end - 1

		if ref == "" || (end < len(runes) && runes[end] == '(') {
			continue
		}

		refs = append(refs, ref)
	}

	return refs
}

func isMentionBoundary(r rune) bool {
	switch r {
	case '(', '[', '{', '"', '\'':
		return true
	default:
		return unicode.IsSpace(r)
	}
}

func isRefRune(r rune) bool {
	switch r {
	case '_', '.', '/', '-':
		return true
	default:
		return unicode.IsLetter(r) || unicode.IsDigit(r)
	}
}
