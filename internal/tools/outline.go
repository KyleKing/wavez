package tools

import (
	"fmt"
	"strings"

	"github.com/kyleking/wavez/internal/codeintel/lang"
)

const (
	// The file length past which a whole-file read comes back as an outline.
	// Measured over this project's thread logs, 572 of 1,506 read calls asked
	// for a whole file, and files longer than this are 28% of those calls and
	// 67% of their bytes.
	outlineMinLines = 300
	// The verbatim head carries the package clause and the imports a run
	// needs to add one.
	outlineHeadLines = 30
	// Fewer declarations than this says too little to be worth a second call
	// for the body.
	outlineMinSymbols = 3
)

// outline is a long file's declarations with the line range of each, in
// place of its text.
//
// It exists because a run that has already found the right file still reads
// it end to end: both h3 lanes under the fuzzy ranking read
// internal/bench/stats.go and its test whole, 341 and 354 lines, to change
// six of them. The head is verbatim so the package clause and the imports
// are there to edit, and every declaration carries the range that reads its
// body, so the follow-up call is a range rather than another whole file.
//
// It returns "" for anything it cannot outline better than the file itself:
// an extension no language claims, a file too short to be worth the second
// call, or source that parses to almost nothing.
func outline(registry *lang.Registry, path string, src []byte) string {
	if registry == nil || !registry.Claims(path) {
		return ""
	}

	lines := strings.Split(string(src), "\n")
	if len(lines) < outlineMinLines {
		return ""
	}

	symbols, err := registry.Extract(path, src)
	if err != nil || len(symbols) < outlineMinSymbols {
		return ""
	}

	head := outlineHead(lines, symbols)

	body := make([]string, 0, len(symbols))
	for i := range symbols {
		body = append(body, fmt.Sprintf("%d-%d\t%s",
			symbols[i].StartLine+1, symbols[i].EndLine+1, signatureOf(symbols[i])))
	}

	return fmt.Sprintf("%s is %d lines, so this is its outline rather than its text: "+
		"lines 1-%d, then every declaration with the range that reads its body.\n\n%s\n\n%s",
		path, len(lines), len(head), strings.Join(head, "\n"), strings.Join(body, "\n"))
}

// outlineHead is the file's text up to its first declaration, which is the
// package clause and the imports, numbered as read numbers every line.
func outlineHead(lines []string, symbols []lang.Symbol) []string {
	stop := len(lines)
	for i := range symbols {
		stop = min(stop, symbols[i].StartLine)
	}

	stop = min(stop, outlineHeadLines)

	head := make([]string, 0, stop)
	for i := range lines[:stop] {
		head = append(head, fmt.Sprintf("%d\t%s", i+1, lines[i]))
	}

	return head
}

// signatureOf is the one line that names a declaration: its first line,
// marked where more of it was cut, and prefixed with the kind only where the
// line does not already say it. A Go struct's recorded signature carries its
// whole body, which is the file back again.
func signatureOf(sym lang.Symbol) string {
	head, rest, cut := strings.Cut(sym.Signature, "\n")

	head = strings.TrimSpace(head)
	if head == "" {
		return sym.Kind + " " + sym.Name
	}

	if cut && strings.TrimSpace(rest) != "" {
		head += " …"
	}

	if first, _, _ := strings.Cut(head, " "); !declarationKinds[first] {
		head = sym.Kind + " " + head
	}

	return head
}

// declarationKinds are the words a signature can already open with, in any
// language here, so the kind is prefixed only where the line does not carry
// it: a Go method's signature opens with func, and its kind is method.
var declarationKinds = map[string]bool{
	lang.KindClass: true, lang.KindFunc: true, lang.KindMethod: true,
	lang.KindType: true, "def": true,
}
