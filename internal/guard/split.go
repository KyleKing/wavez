package guard

import "strings"

const (
	singleCharOp = 1
	twoCharOp    = 2
)

// substScanner walks a command line rune by rune, tracking quote state so
// that command substitutions found outside single quotes can be pulled out
// and classified on their own.
type substScanner struct {
	runes    []rune
	buf      strings.Builder
	subs     []string
	inSingle bool
	inDouble bool
}

// extractSubstitutions pulls every $() or backtick command substitution out
// of s, replacing each with a single space, and returns the remaining text
// alongside the substitutions' inner command text. Substitutions run a
// command regardless of what encloses them, so they are classified
// separately rather than as part of the text around them.
//
//nolint:nonamedreturns // gocritic's unnamedResult wants these two same-shaped results named
func extractSubstitutions(s string) (outer string, subs []string) {
	sc := &substScanner{runes: []rune(s)}
	for i := 0; i < len(sc.runes); i++ {
		i = sc.step(i)
	}

	return sc.buf.String(), sc.subs
}

func (sc *substScanner) step(i int) int {
	c := sc.runes[i]
	switch {
	case sc.inSingle:
		sc.buf.WriteRune(c)
		if c == '\'' {
			sc.inSingle = false
		}

		return i
	case sc.inDouble:
		return sc.stepDouble(i)
	case c == '\'':
		sc.inSingle = true
		sc.buf.WriteRune(c)

		return i
	case c == '"':
		sc.inDouble = true
		sc.buf.WriteRune(c)

		return i
	case c == '$' && i+1 < len(sc.runes) && sc.runes[i+1] == '(':
		return sc.stepParenSubst(i)
	case c == '`':
		return sc.stepBacktick(i)
	default:
		sc.buf.WriteRune(c)

		return i
	}
}

func (sc *substScanner) stepDouble(i int) int {
	c := sc.runes[i]
	if c == '\\' && i+1 < len(sc.runes) {
		sc.buf.WriteRune(c)
		i++
		sc.buf.WriteRune(sc.runes[i])

		return i
	}
	sc.buf.WriteRune(c)
	if c == '"' {
		sc.inDouble = false
	}

	return i
}

func (sc *substScanner) stepParenSubst(i int) int {
	end := matchParen(sc.runes, i+1)
	if end == -1 {
		sc.buf.WriteRune('$')

		return i
	}
	sc.subs = append(sc.subs, string(sc.runes[i+2:end]))
	sc.buf.WriteRune(' ')

	return end
}

func (sc *substScanner) stepBacktick(i int) int {
	end := indexRuneFrom(sc.runes, i+1, '`')
	if end == -1 {
		sc.buf.WriteRune('`')

		return i
	}
	sc.subs = append(sc.subs, string(sc.runes[i+1:end]))
	sc.buf.WriteRune(' ')

	return end
}

func matchParen(runes []rune, open int) int {
	depth := 1
	for i := open + 1; i < len(runes); i++ {
		switch runes[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}

	return -1
}

func indexRuneFrom(runes []rune, start int, target rune) int {
	for i := start; i < len(runes); i++ {
		if runes[i] == target {
			return i
		}
	}

	return -1
}

// splitSequence splits s on the sequential operators `;`, `&&`, `||`, and a
// lone backgrounding `&`, honoring quotes. Each result may still contain
// pipes; splitPipeline handles those.
func splitSequence(s string) []string {
	return splitTopLevel(s, func(runes []rune, i int) (int, bool) {
		switch {
		// A newline separates commands exactly as `;` does, which is what
		// lets Classify read a whole script and not just one command line.
		case runes[i] == ';' || runes[i] == '\n':
			return singleCharOp, true
		case runes[i] == '&' && i+1 < len(runes) && runes[i+1] == '&':
			return twoCharOp, true
		case runes[i] == '|' && i+1 < len(runes) && runes[i+1] == '|':
			return twoCharOp, true
		case runes[i] == '&' && isRedirAmp(runes, i):
			return 0, false
		case runes[i] == '&':
			return singleCharOp, true
		default:
			return 0, false
		}
	})
}

// isRedirAmp reports whether the '&' at runes[i] is part of a redirection
// target rather than a backgrounding operator: it is immediately preceded by
// an unquoted '>' (possibly with a digit fd prefix, as in `2>&1`) or
// immediately followed by '>', as in bash's `&>file`.
func isRedirAmp(runes []rune, i int) bool {
	if i+1 < len(runes) && runes[i+1] == '>' {
		return true
	}

	return i > 0 && runes[i-1] == '>'
}

// splitPipeline splits a single pipeline on `|`, honoring quotes.
func splitPipeline(s string) []string {
	return splitTopLevel(s, func(runes []rune, i int) (int, bool) {
		if runes[i] == '|' {
			return singleCharOp, true
		}

		return 0, false
	})
}

func splitTopLevel(s string, isSep func(runes []rune, i int) (int, bool)) []string {
	var out []string
	var buf strings.Builder
	var inSingle, inDouble bool
	runes := []rune(s)

	flush := func() {
		f := strings.TrimSpace(buf.String())
		if f != "" {
			out = append(out, f)
		}
		buf.Reset()
	}

	for i := 0; i < len(runes); i++ {
		c := runes[i]

		if inSingle {
			buf.WriteRune(c)
			if c == '\'' {
				inSingle = false
			}

			continue
		}
		if inDouble {
			buf.WriteRune(c)
			if c == '"' {
				inDouble = false
			}

			continue
		}
		if c == '\'' {
			inSingle = true
			buf.WriteRune(c)

			continue
		}
		if c == '"' {
			inDouble = true
			buf.WriteRune(c)

			continue
		}

		if n, ok := isSep(runes, i); ok {
			flush()
			i += n - 1

			continue
		}
		buf.WriteRune(c)
	}
	flush()

	return out
}

// tokenize splits a single command into words, stripping quote characters.
// It does not interpret escapes or expansions beyond that.
func tokenize(s string) []string {
	var tokens []string
	var buf strings.Builder
	var inSingle, inDouble bool
	has := false

	flush := func() {
		if has {
			tokens = append(tokens, buf.String())
			buf.Reset()
			has = false
		}
	}

	for _, c := range s {
		switch {
		case inSingle:
			if c == '\'' {
				inSingle = false
			} else {
				buf.WriteRune(c)
			}
		case inDouble:
			if c == '"' {
				inDouble = false
			} else {
				buf.WriteRune(c)
			}
		case c == '\'':
			inSingle = true
			has = true
		case c == '"':
			inDouble = true
			has = true
		case c == ' ' || c == '\t' || c == '\n':
			flush()
		default:
			buf.WriteRune(c)
			has = true
		}
	}
	flush()

	return tokens
}
