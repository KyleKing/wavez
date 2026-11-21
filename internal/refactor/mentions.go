package refactor

import (
	"bytes"
	"go/scanner"
	"go/token"
	"path"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Mention is one line where a renamed symbol's old name still appears in a
// comment or a string literal.
type Mention struct {
	File string
	Line int
}

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

		for _, offset := range wordLines(lit, word) {
			line := file.Line(pos) + offset
			if len(out) == 0 || out[len(out)-1].Line != line {
				out = append(out, Mention{File: rel, Line: line})
			}
		}
	}
}

// wordLines returns, for each word-bounded occurrence of word in text, how
// many lines into text it sits.
func wordLines(text, word string) []int {
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

		out = append(out, strings.Count(text[:start], "\n"))
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
