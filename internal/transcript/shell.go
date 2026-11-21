package transcript

import "strings"

// simpleCommand is one command of a shell line: its words, and whether it
// redirects output into a file.
type simpleCommand struct {
	words  []string
	writes bool
}

// harmlessRedirects are the targets a read-only command may still redirect
// to.
var harmlessRedirects = []string{"/dev/null", "&1", "&2"}

// splitter walks a shell line once, cutting it into simple commands at the
// control operators and into words at unquoted whitespace. It is not a
// shell parser: it knows quotes, operators, and redirects, which is what
// classifying a command needs.
type splitter struct {
	word  strings.Builder
	out   []simpleCommand
	cur   simpleCommand
	quote rune
	open  bool
}

// splitCommands breaks a shell line into its simple commands.
func splitCommands(line string) []simpleCommand {
	var s splitter

	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		i += s.step(runes, i)
	}

	s.endCommand()

	return s.out
}

// step consumes the rune at i and returns how many further runes it took.
func (s *splitter) step(runes []rune, i int) int {
	r := runes[i]

	switch {
	case s.quote != 0:
		if r == s.quote {
			s.quote = 0
		} else {
			s.word.WriteRune(r)
		}
	case r == '\'' || r == '"':
		s.quote, s.open = r, true
	case r == '\\' && i+1 < len(runes):
		s.word.WriteRune(runes[i+1])
		s.open = true

		return 1
	case r == ' ' || r == '\t':
		s.endWord()
	case r == '>':
		return s.redirect(runes, i)
	case isOperator(r):
		if r == '&' && i+1 < len(runes) && runes[i+1] == '>' {
			return s.redirect(runes, i+1) + 1
		}

		s.endCommand()
	default:
		s.word.WriteRune(r)
		s.open = true
	}

	return 0
}

func isOperator(r rune) bool {
	return strings.ContainsRune("&|;\n()", r)
}

// redirect consumes a redirect operator at i and its target, recording a
// write unless the target is harmless. A file descriptor glued to the
// operator (`2>`) has already been collected as a word and is dropped.
func (s *splitter) redirect(runes []rune, i int) int {
	if isDigits(s.word.String()) {
		s.word.Reset()
		s.open = false
	}

	s.endWord()

	j := i + 1
	for j < len(runes) && (runes[j] == '>' || runes[j] == ' ') {
		j++
	}

	start := j
	for j < len(runes) && !strings.ContainsRune(" \t\n;|)", runes[j]) {
		if runes[j] == '&' && j > start {
			break
		}

		j++
	}

	target := strings.Trim(string(runes[start:j]), `"'`)
	if !isHarmless(target) {
		s.cur.writes = true
	}

	return j - i - 1
}

func isHarmless(target string) bool {
	for _, h := range harmlessRedirects {
		if target == h {
			return true
		}
	}

	return false
}

func isDigits(word string) bool {
	if word == "" {
		return false
	}

	for _, r := range word {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}

func (s *splitter) endWord() {
	if !s.open {
		return
	}

	s.cur.words = append(s.cur.words, s.word.String())
	s.word.Reset()
	s.open = false
}

func (s *splitter) endCommand() {
	s.endWord()

	if len(s.cur.words) > 0 || s.cur.writes {
		s.out = append(s.out, s.cur)
	}

	s.cur = simpleCommand{}
}
