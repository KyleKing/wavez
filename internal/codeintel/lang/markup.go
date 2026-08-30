package lang

import (
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_css "github.com/tree-sitter/tree-sitter-css/bindings/go"
	tree_sitter_html "github.com/tree-sitter/tree-sitter-html/bindings/go"
)

// css indexes stylesheets. Its symbols are selectors, which is what a run
// looking for a class asks about, and indexing the file at all is the larger
// half: a file the index does not hold is invisible to literal search, and a
// search that answered "no matches" for a rule sitting in main.css sent one
// run to a confident wrong conclusion.
type css struct {
	grammar *tree_sitter.Language
}

func newCSS() *css {
	return &css{grammar: tree_sitter.NewLanguage(tree_sitter_css.Language())}
}

func (*css) Extensions() []string { return []string{".css"} }

var cssKinds = map[string]string{"rule_set": KindRule}

func (c *css) Extract(src []byte) ([]Symbol, error) {
	return walk(src, c.grammar, cssKinds, func(kind string, n *tree_sitter.Node) (Symbol, bool) {
		selectors := n.ChildByFieldName("selectors")
		if selectors == nil {
			return Symbol{}, false
		}

		span := nodeRange(n)
		name := strings.Join(strings.Fields(selectors.Utf8Text(src)), " ")

		return Symbol{
			Kind:      kind,
			Name:      name,
			StartByte: span.StartByte,
			EndByte:   span.EndByte,
			StartLine: span.StartLine,
			EndLine:   span.EndLine,
			Signature: name,
			Doc:       leadingComments(src, n, stripJSDoc),
		}, true
	})
}

// markup indexes HTML and the templates written in it. A template language's
// own tags are not HTML and the parser recovers past them rather than
// understanding them, so only the elements it does resolve become symbols and
// the rest of the file is reached through its text.
type markup struct {
	grammar    *tree_sitter.Language
	extensions []string
}

func newHTML() *markup {
	return &markup{
		grammar:    tree_sitter.NewLanguage(tree_sitter_html.Language()),
		extensions: []string{".html", ".jinja"},
	}
}

func (m *markup) Extensions() []string { return m.extensions }

var markupKinds = map[string]string{"element": KindElement}

func (m *markup) Extract(src []byte) ([]Symbol, error) {
	return walk(src, m.grammar, markupKinds, func(kind string, n *tree_sitter.Node) (Symbol, bool) {
		id, ok := elementID(src, n)
		if !ok {
			return Symbol{}, false
		}

		span := nodeRange(n)

		return Symbol{
			Kind:      kind,
			Name:      id,
			StartByte: span.StartByte,
			EndByte:   span.EndByte,
			StartLine: span.StartLine,
			EndLine:   span.EndLine,
			Signature: "#" + id,
		}, true
	})
}

// elementID is the value of an element's id attribute, which is the only
// part of a document a run can name and expect to find once.
func elementID(src []byte, n *tree_sitter.Node) (string, bool) {
	tag := n.NamedChild(0)
	if tag == nil {
		return "", false
	}

	for i := range tag.NamedChildCount() {
		attr := tag.NamedChild(i)
		if attr.Kind() != "attribute" {
			continue
		}

		name := attr.NamedChild(0)
		if name == nil || name.Utf8Text(src) != "id" {
			continue
		}

		if value := attributeValue(src, attr); value != "" {
			return value, true
		}
	}

	return "", false
}

func attributeValue(src []byte, attr *tree_sitter.Node) string {
	for i := range attr.NamedChildCount() {
		switch child := attr.NamedChild(i); child.Kind() {
		case "quoted_attribute_value":
			if inner := child.NamedChild(0); inner != nil {
				return inner.Utf8Text(src)
			}
		case "attribute_value":
			return child.Utf8Text(src)
		}
	}

	return ""
}
