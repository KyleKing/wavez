package lang

import (
	"strings"

	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// typescript indexes one TypeScript dialect. The grammar differs between
// `.ts` and `.tsx`, because the same angle brackets are a type assertion in
// one and an element in the other, and a Language is chosen by extension
// with no source to sniff.
type typescript struct {
	grammar    *tree_sitter.Language
	extensions []string
}

func newTypeScript() *typescript {
	return &typescript{
		grammar:    tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript()),
		extensions: []string{".ts"},
	}
}

func newTSX() *typescript {
	return &typescript{
		grammar:    tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTSX()),
		extensions: []string{".tsx"},
	}
}

func (t *typescript) Extensions() []string { return t.extensions }

var typescriptKinds = map[string]string{
	"class_declaration":      KindClass,
	"function_declaration":   KindFunc,
	"interface_declaration":  KindType,
	"method_definition":      KindMethod,
	"type_alias_declaration": KindType,
}

func (t *typescript) Extract(src []byte) ([]Symbol, error) {
	return walk(src, t.grammar, typescriptKinds, func(kind string, n *tree_sitter.Node) (Symbol, bool) {
		name := n.ChildByFieldName("name")
		if name == nil {
			return Symbol{}, false
		}

		span := nodeRange(n)

		return Symbol{
			Kind:      kind,
			Name:      name.Utf8Text(src),
			StartByte: span.StartByte,
			EndByte:   span.EndByte,
			StartLine: span.StartLine,
			EndLine:   span.EndLine,
			Signature: signatureBefore(src, n, typescriptBody(kind, n)),
			Doc:       leadingComments(src, documented(n), stripJSDoc),
		}, true
	})
}

// typescriptBody is where the declaration's signature stops. A type alias
// has no body and its value is the definition, so its whole text is the
// signature, the way a Go struct's is.
func typescriptBody(kind string, n *tree_sitter.Node) *tree_sitter.Node {
	if kind == KindType && n.Kind() == "type_alias_declaration" {
		return nil
	}

	return n.ChildByFieldName("body")
}

// documented is the node a declaration's doc comment sits above. `export`
// wraps the declaration, so the comment is the export statement's sibling
// and not the declaration's: without this every exported symbol, which in
// TypeScript is very nearly all of them, indexes with no doc at all.
func documented(n *tree_sitter.Node) *tree_sitter.Node {
	if parent := n.Parent(); parent != nil && parent.Kind() == "export_statement" {
		return parent
	}

	return n
}

// stripJSDoc removes the comment markers from one line, including the
// leading asterisk a JSDoc block puts on every line after the first.
func stripJSDoc(line string) string {
	line = strings.TrimPrefix(line, "//")
	line = strings.TrimPrefix(line, "/**")
	line = strings.TrimPrefix(line, "/*")
	line = strings.TrimSuffix(line, "*/")
	line = strings.TrimSpace(line)

	return strings.TrimSpace(strings.TrimPrefix(line, "*"))
}
