package lang

import (
	"fmt"
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
)

// visitFunc reports the Symbol for a matched node, or false to skip it.
type visitFunc func(kind string, n *tree_sitter.Node) (Symbol, bool)

// walk runs a tree-sitter parse of src with grammar and calls visit for
// every node whose Kind() is in kinds, collecting the results.
func walk(src []byte, grammar *tree_sitter.Language, kinds map[string]string, visit visitFunc) ([]Symbol, error) {
	parser := tree_sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(grammar); err != nil {
		return nil, fmt.Errorf("setting tree-sitter language: %w", err)
	}
	tree := parser.Parse(src, nil)
	if tree == nil {
		return nil, nil
	}
	defer tree.Close()

	var symbols []Symbol
	var recurse func(n *tree_sitter.Node)
	recurse = func(n *tree_sitter.Node) {
		if kind, ok := kinds[n.Kind()]; ok {
			if sym, ok := visit(kind, n); ok {
				symbols = append(symbols, sym)
			}
		}
		count := n.NamedChildCount()
		for i := range count {
			recurse(n.NamedChild(i))
		}
	}
	recurse(tree.RootNode())

	return symbols, nil
}

// nodeSpan is n's byte and 1-indexed line range.
type nodeSpan struct {
	StartByte uint
	EndByte   uint
	StartLine int
	EndLine   int
}

func nodeRange(n *tree_sitter.Node) nodeSpan {
	return nodeSpan{
		StartByte: n.StartByte(),
		EndByte:   n.EndByte(),
		StartLine: int(n.StartPosition().Row) + 1,
		EndLine:   int(n.EndPosition().Row) + 1,
	}
}

// signatureBefore returns src trimmed between decl's start and the byte
// where body begins, i.e. the declaration without its implementation.
func signatureBefore(src []byte, decl, body *tree_sitter.Node) string {
	if body == nil {
		return strings.TrimSpace(decl.Utf8Text(src))
	}

	return strings.TrimSpace(string(src[decl.StartByte():body.StartByte()]))
}

// leadingComments collects comment siblings immediately above n with no
// blank line between them (the doc-comment convention), in source order,
// joined with newlines and stripped of comment markers.
func leadingComments(src []byte, n *tree_sitter.Node, commentKind string, strip func(string) string) string {
	var lines []string
	expectEndLine := int(n.StartPosition().Row) - 1
	sib := n.PrevSibling()
	for sib != nil && sib.Kind() == commentKind && int(sib.EndPosition().Row) == expectEndLine {
		lines = append([]string{strip(strings.TrimSpace(sib.Utf8Text(src)))}, lines...)
		expectEndLine = int(sib.StartPosition().Row) - 1
		sib = sib.PrevSibling()
	}

	return strings.TrimSpace(strings.Join(lines, "\n"))
}
