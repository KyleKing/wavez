package lang

import (
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
)

type python struct {
	grammar *tree_sitter.Language
}

func newPython() *python {
	return &python{grammar: tree_sitter.NewLanguage(tree_sitter_python.Language())}
}

func (*python) Extensions() []string { return []string{".py"} }

var pythonKinds = map[string]string{
	"function_definition": KindFunc,
	"class_definition":    KindClass,
}

func (p *python) Extract(src []byte) ([]Symbol, error) {
	return walk(src, p.grammar, pythonKinds, func(kind string, n *tree_sitter.Node) (Symbol, bool) {
		name := n.ChildByFieldName("name")
		if name == nil {
			return Symbol{}, false
		}
		body := n.ChildByFieldName("body")
		span := nodeRange(n)

		return Symbol{
			Kind:      kind,
			Name:      name.Utf8Text(src),
			StartByte: span.StartByte,
			EndByte:   span.EndByte,
			StartLine: span.StartLine,
			EndLine:   span.EndLine,
			Signature: signatureBefore(src, n, body),
			Doc:       pythonDocstring(src, body),
		}, true
	})
}

// pythonDocstring returns the string literal that is the first statement in
// body, Python's docstring convention, rather than a comment above the
// declaration.
func pythonDocstring(src []byte, body *tree_sitter.Node) string {
	if body == nil || body.NamedChildCount() == 0 {
		return ""
	}
	first := body.NamedChild(0)
	if first.Kind() != "expression_statement" || first.NamedChildCount() == 0 {
		return ""
	}
	str := first.NamedChild(0)
	if str.Kind() != "string" {
		return ""
	}
	for i := range str.NamedChildCount() {
		if part := str.NamedChild(i); part.Kind() == "string_content" {
			return strings.TrimSpace(part.Utf8Text(src))
		}
	}

	return ""
}
