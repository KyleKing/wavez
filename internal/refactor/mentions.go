package refactor

import (
	"bytes"
	"go/scanner"
	"go/token"
	"path"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Mention is one line where a renamed symbol's old name still appears in a
// comment or a string literal.
type Mention struct {
	File string
	// Text is the line, trimmed and cut to maxMentionText bytes.
	Text string
	Line int
}

// maxMentionText bounds the text of a line a report repeats.
const maxMentionText = 100

// mentionsIn finds the lines of a file where word stands alone inside a
// comment or string literal. Those are the occurrences gopls leaves, since
// nothing in the type information ties them to the symbol.
func mentionsIn(rel string, src []byte, word string) []Mention {
	if !bytes.Contains(src, []byte(word)) {
		return nil
	}

	fset := token.NewFileSet()
	file := fset.AddFile(rel, -1, len(src))

	var (
		scan scanner.Scanner
		out  []Mention
	)

	scan.Init(file, src, nil, scanner.ScanComments)

	for {
		pos, tok, lit := scan.Scan()
		if tok == token.EOF {
			return out
		}

		if tok != token.COMMENT && tok != token.STRING {
			continue
		}

		for _, start := range wordStarts(lit, word) {
			at := file.Offset(pos) + start
			line := file.Line(pos) + strings.Count(lit[:start], "\n")

			if len(out) == 0 || out[len(out)-1].Line != line {
				out = append(out, Mention{File: rel, Line: line, Text: lineText(src, at)})
			}
		}
	}
}

// lineText is the line of src that holds offset at, trimmed and cut short.
func lineText(src []byte, at int) string {
	start := bytes.LastIndexByte(src[:at], '\n') + 1

	end := len(src)
	if i := bytes.IndexByte(src[at:], '\n'); i >= 0 {
		end = at + i
	}

	text := strings.TrimSpace(string(src[start:end]))
	if len(text) > maxMentionText {
		text = strings.ToValidUTF8(text[:maxMentionText], "") + "..."
	}

	return text
}

// renameMentions rewrites, in the files a rename changed, the comments and
// strings that unmistakably name the renamed symbol, and returns how many
// it rewrote. A mention is unmistakable when it is written as a call or in
// backquotes, or, in a comment, when the name could not be an ordinary
// word. A struct tag or a key is a string that is neither, so it stays.
func (s *session) renameMentions(changed []string, old, replacement string) (int, error) {
	total := 0

	for _, file := range changed {
		src, err := s.tree.read(file)
		if err != nil {
			continue
		}

		at := symbolMentions(src, old)
		if len(at) == 0 {
			continue
		}

		for _, i := range slices.Backward(at) {
			src = splice(src, i, i+len(old), replacement)
		}

		if err := s.snap.write(file, src); err != nil {
			return total, err
		}

		total += len(at)
	}

	return total, nil
}

// symbolMentions returns the offsets in src where word names the symbol in
// a comment or string, by the rule renameMentions gives.
func symbolMentions(src []byte, word string) []int {
	if !bytes.Contains(src, []byte(word)) {
		return nil
	}

	fset := token.NewFileSet()
	file := fset.AddFile("", -1, len(src))

	var (
		scan scanner.Scanner
		out  []int
	)

	scan.Init(file, src, nil, scanner.ScanComments)

	shaped := identifierShaped(word)

	for {
		pos, tok, lit := scan.Scan()
		if tok == token.EOF {
			return out
		}

		if tok != token.COMMENT && tok != token.STRING {
			continue
		}

		base := file.Offset(pos)

		for _, start := range wordStarts(lit, word) {
			end := start + len(word)
			called := end < len(lit) && lit[end] == '('
			quoted := start > 0 && lit[start-1] == '`' && end < len(lit) && lit[end] == '`'

			if called || quoted || tok == token.COMMENT && shaped {
				out = append(out, base+start)
			}
		}
	}
}

// identifierShaped says whether a name could only be an identifier: it has
// an underscore, a digit, or a capital after its first letter.
func identifierShaped(name string) bool {
	for i, r := range name {
		if r == '_' || unicode.IsDigit(r) || i > 0 && unicode.IsUpper(r) {
			return true
		}
	}

	return false
}

// wordStarts returns where word occurs in text with no letter, digit, or
// underscore on either side.
func wordStarts(text, word string) []int {
	var out []int

	for from := 0; ; {
		i := strings.Index(text[from:], word)
		if i < 0 {
			return out
		}

		start := from + i
		end := start + len(word)
		from = end

		before, _ := utf8.DecodeLastRuneInString(text[:start])
		after, _ := utf8.DecodeRuneInString(text[end:])

		if start > 0 && isWordRune(before) || end < len(text) && isWordRune(after) {
			continue
		}

		out = append(out, start)
	}
}

func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// mentions scans the module for leftover uses of word in comments and
// strings, ordered so the likeliest to mean the renamed symbol come first:
// the files the rename changed, then the declaring package, then the rest.
func (s *session) mentions(files []string, word, declDir string, changed map[string]bool) []Mention {
	const (
		rankChanged = iota
		rankPackage
		rankModule
	)

	rank := func(file string) int {
		switch {
		case changed[file]:
			return rankChanged
		case path.Dir(file) == declDir:
			return rankPackage
		default:
			return rankModule
		}
	}

	sorted := append([]string(nil), files...)
	sort.SliceStable(sorted, func(i, j int) bool { return rank(sorted[i]) < rank(sorted[j]) })

	var out []Mention

	for _, file := range sorted {
		src, err := s.tree.read(file)
		if err != nil {
			continue
		}

		out = append(out, mentionsIn(file, src, word)...)
	}

	return out
}
