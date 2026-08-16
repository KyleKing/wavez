package lang

import (
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
)

type golang struct {
	grammar *tree_sitter.Language
}

func newGo() *golang {
	return &golang{grammar: tree_sitter.NewLanguage(tree_sitter_go.Language())}
}

func (*golang) Extensions() []string { return []string{".go"} }

var goKinds = map[string]string{
	"function_declaration": KindFunc,
	"method_declaration":   KindMethod,
	"type_declaration":     KindType,
}

func (g *golang) Extract(src []byte) ([]Symbol, error) {
	return walk(src, g.grammar, goKinds, func(kind string, n *tree_sitter.Node) (Symbol, bool) {
		switch kind {
		case KindFunc, KindMethod:
			return goFuncSymbol(src, kind, n)
		case KindType:
			return goTypeSymbol(src, n)
		}

		return Symbol{}, false
	})
}

func goFuncSymbol(src []byte, kind string, n *tree_sitter.Node) (Symbol, bool) {
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
		Doc:       leadingComments(src, n, "comment", stripGoComment),
	}, true
}

// goTypeSymbol handles only the single-spec form (type Foo struct{...}),
// which covers the overwhelming majority of Go type declarations; a
// parenthesized group (type (A struct{}; B int)) is left to the resolver
// this pass leaves as a seam.
func goTypeSymbol(src []byte, n *tree_sitter.Node) (Symbol, bool) {
	if n.NamedChildCount() != 1 {
		return Symbol{}, false
	}
	spec := n.NamedChild(0)
	if spec.Kind() != "type_spec" {
		return Symbol{}, false
	}
	name := spec.ChildByFieldName("name")
	if name == nil {
		return Symbol{}, false
	}
	span := nodeRange(n)

	return Symbol{
		Kind:      KindType,
		Name:      name.Utf8Text(src),
		StartByte: span.StartByte,
		EndByte:   span.EndByte,
		StartLine: span.StartLine,
		EndLine:   span.EndLine,
		Signature: strings.TrimSpace(n.Utf8Text(src)),
		Doc:       leadingComments(src, n, "comment", stripGoComment),
	}, true
}

func stripGoComment(line string) string {
	line = strings.TrimPrefix(line, "//")
	line = strings.TrimPrefix(line, "/*")
	line = strings.TrimSuffix(line, "*/")

	return strings.TrimSpace(line)
}
