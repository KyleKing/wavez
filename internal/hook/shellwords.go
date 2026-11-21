package hook

import "strings"

// splitter is the state of one pass over a command line.
type splitter struct {
	segments [][]string
	words    []string
	word     strings.Builder
	quote    rune
	// unclosed is true when a substitution never ended.
	unclosed bool
	// open is true once the current word has begun, which an empty pair of
	// quotes does without adding a rune.
	open bool
}

// shellSegments splits a shell command line into its simple commands, each
// a list of words with the quoting removed. It reads enough of the grammar
// to find a program and its arguments: quotes, backslashes, and the
// operators that end a command. A command substitution stays one word, text
// and all, so what it would expand to is visibly unknown. A command that
// ends inside a quote or a substitution was not read, and reports false.
func shellSegments(command string) ([][]string, bool) {
	var (
		s    splitter
		runs = []rune(command)
	)

	for i := 0; i < len(runs); i++ {
		if s.quote != 0 {
			i = s.quoted(runs, i)

			continue
		}

		i = s.bare(runs, i)
	}

	s.endSegment()

	return s.segments, s.quote == 0 && !s.unclosed
}

// bare takes the rune at i outside quotes and returns the index of the
// last rune it used.
func (s *splitter) bare(runs []rune, i int) int {
	r, more := runs[i], i+1 < len(runs)

	switch {
	case r == '\\' && more:
		s.add(runs[i+1])

		return i + 1
	case r == '$' && more && runs[i+1] == '(':
		return s.substitution(runs, i)
	case r == '\'' || r == '"':
		s.quote, s.open = r, true
	case r == ' ' || r == '\t':
		s.endWord()
	case strings.ContainsRune("|&;\n()", r):
		s.endSegment()
	default:
		s.add(r)
	}

	return i
}

// quoted takes the rune at i inside quotes, closing them on their own
// mark, and returns the index of the last rune it used. Inside double quotes
// a backslash is kept before anything it does not escape, which is how
// "\bOld\b" reaches sed whole.
func (s *splitter) quoted(runs []rune, i int) int {
	r := runs[i]

	switch {
	case r == s.quote:
		s.quote = 0
	case r == '\\' && s.quote == '"' && i+1 < len(runs) && strings.ContainsRune("$`\"\\", runs[i+1]):
		s.add(runs[i+1])

		return i + 1
	default:
		s.add(r)
	}

	return i
}

func (s *splitter) add(r rune) {
	s.word.WriteRune(r)
	s.open = true
}

// substitution copies a $( ... ) into the current word whole and returns
// the index of its closing parenthesis.
func (s *splitter) substitution(runs []rune, at int) int {
	depth := 0

	for i := at; i < len(runs); i++ {
		s.add(runs[i])

		switch runs[i] {
		case '(':
			depth++
		case ')':
			depth--
		}

		if depth == 0 && i > at {
			return i
		}
	}

	s.unclosed = true

	return len(runs)
}

func (s *splitter) endWord() {
	if s.open {
		s.words = append(s.words, s.word.String())
	}

	s.word.Reset()
	s.open = false
}

func (s *splitter) endSegment() {
	s.endWord()

	if len(s.words) > 0 {
		s.segments = append(s.segments, s.words)
	}

	s.words = nil
}
