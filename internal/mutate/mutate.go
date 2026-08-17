// Package mutate produces deliberate small changes to Go source so a test
// suite can be asked the question coverage cannot answer: not whether a
// line ran, but whether anything checked what it did.
package mutate

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"

	"github.com/kyleking/wavez/internal/tool"
)

// Operator names one kind of change. The set is deliberately small: these
// are the shapes that produce a compiling program every time, so a surviving
// mutant always means the suite ignored a real behavior change rather than
// that the mutant failed to build.
const (
	// OpBoundary widens or narrows a comparison (< becomes <=).
	OpBoundary = "boundary"
	// OpNegate flips an equality or ordering test (== becomes !=).
	OpNegate = "negate"
	// OpBool flips a true or false literal.
	OpBool = "bool"
)

// swaps maps each mutable token to what it becomes. A token appearing here
// is mutated everywhere it falls inside a requested range.
var swaps = map[token.Token]struct {
	op string
	to token.Token
}{
	token.LSS: {OpBoundary, token.LEQ},
	token.LEQ: {OpBoundary, token.LSS},
	token.GTR: {OpBoundary, token.GEQ},
	token.GEQ: {OpBoundary, token.GTR},
	token.EQL: {OpNegate, token.NEQ},
	token.NEQ: {OpNegate, token.EQL},
}

// Mutant is one source file with one change applied. Source is the whole
// file, so applying a mutant is a single write and no two mutants interact.
type Mutant struct {
	Path   string
	Op     string
	Before string
	After  string
	Source []byte
	Line   int
}

// Describe renders a mutant the way a failing test names itself, so a
// survivor reads as a defect rather than as tooling output.
func (m Mutant) Describe() string {
	return fmt.Sprintf("%s:%d %s %s -> %s", m.Path, m.Line, m.Op, m.Before, m.After)
}

// Mutants returns every mutant of src whose change falls inside ranges.
// Passing no ranges mutates the whole file. Path is used for reporting only;
// src is what gets parsed.
func Mutants(path string, src []byte, ranges []tool.LineRange) ([]Mutant, error) {
	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	var out []Mutant

	ast.Inspect(file, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.BinaryExpr:
			if swap, ok := swaps[node.Op]; ok {
				out = appendMutant(out, fset, path, src, ranges,
					node.OpPos, len(node.Op.String()), swap.op, node.Op.String(), swap.to.String())
			}
		case *ast.Ident:
			if flipped, ok := flipBool(node.Name); ok {
				out = appendMutant(out, fset, path, src, ranges,
					node.NamePos, len(node.Name), OpBool, node.Name, flipped)
			}
		}

		return true
	})

	sort.SliceStable(out, func(i, j int) bool { return out[i].Line < out[j].Line })

	return out, nil
}

func flipBool(name string) (string, bool) {
	switch name {
	case "true":
		return "false", true
	case "false":
		return "true", true
	default:
		return "", false
	}
}

// appendMutant splices replacement over the width bytes at pos, keeping the
// rest of the file byte-identical. Rewriting bytes rather than reprinting
// the AST means a mutant differs from its original in exactly one place,
// which is what makes a survivor attributable.
func appendMutant(
	out []Mutant, fset *token.FileSet, path string, src []byte, ranges []tool.LineRange,
	pos token.Pos, width int, op, before, after string,
) []Mutant {
	position := fset.Position(pos)
	if !inRanges(position.Line, ranges) {
		return out
	}

	offset := position.Offset

	mutated := make([]byte, 0, len(src)-width+len(after))
	mutated = append(mutated, src[:offset]...)
	mutated = append(mutated, after...)
	mutated = append(mutated, src[offset+width:]...)

	return append(out, Mutant{
		Path:   path,
		Line:   position.Line,
		Op:     op,
		Before: before,
		After:  after,
		Source: mutated,
	})
}

// inRanges reports whether line falls in any range. No ranges means the
// whole file, so a caller with no line information still gets mutants.
func inRanges(line int, ranges []tool.LineRange) bool {
	if len(ranges) == 0 {
		return true
	}

	for _, r := range ranges {
		if line >= r.Start && line <= r.End {
			return true
		}
	}

	return false
}
