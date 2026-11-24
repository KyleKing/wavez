package refactor

import (
	"fmt"
	"go/ast"
	"go/token"
	"strconv"
)

// lineSpan is an inclusive range of lines, counted from one.
type lineSpan struct {
	from, to int
}

func (l lineSpan) String() string {
	if l.from == l.to {
		return strconv.Itoa(l.from)
	}

	return fmt.Sprintf("%d-%d", l.from, l.to)
}

func (l lineSpan) covers(o lineSpan) bool { return l.from <= o.from && o.to <= l.to }

func (l lineSpan) overlaps(o lineSpan) bool { return l.from <= o.to && o.from <= l.to }

// picked is the run of whole statements a line range selects for
// extraction, and the function declaration holding them.
type picked struct {
	fn    *ast.FuncDecl
	stmts []ast.Stmt
}

// start and end are the byte offsets of the first statement's start and
// the last one's end.
func (p *picked) start(pf *parsedFile) int { return pf.offset(p.stmts[0].Pos()) }

func (p *picked) end(pf *parsedFile) int { return pf.offset(p.stmts[len(p.stmts)-1].End()) }

func extractRefusal(pf *parsedFile, want lineSpan, reason string) *ExtractError {
	return &ExtractError{At: pf.rel + ":" + want.String(), Reason: reason}
}

func nodeLines(pf *parsedFile, n ast.Node) lineSpan {
	return lineSpan{from: pf.line(n.Pos()), to: pf.line(n.End())}
}

// pickStatements finds the statements a line range selects: every
// statement of one block that the range overlaps, each of them whole. It
// descends through a statement the range sits inside, so lines inside a
// loop body select statements of that body.
func pickStatements(pf *parsedFile, want lineSpan) (*picked, error) {
	fn := enclosingFunc(pf, want)
	if fn == nil {
		return nil, extractRefusal(pf, want, "the lines are not inside a function body; "+
			"pick lines between a function's braces")
	}

	list := fn.Body.List

	for {
		var hit []ast.Stmt

		for _, stmt := range list {
			if want.overlaps(nodeLines(pf, stmt)) {
				hit = append(hit, stmt)
			}
		}

		if len(hit) == 0 {
			return nil, extractRefusal(pf, want, "the lines hold no statement")
		}

		whole := lineSpan{from: pf.line(hit[0].Pos()), to: pf.line(hit[len(hit)-1].End())}
		if want.covers(whole) {
			return &picked{fn: fn, stmts: hit}, nil
		}

		inner := innerList(pf, hit, want)
		if inner == nil {
			wider := lineSpan{from: min(want.from, whole.from), to: max(want.to, whole.to)}

			return nil, extractRefusal(pf, want, fmt.Sprintf("the lines cut a statement in two; extract lines %s, "+
				"or lines inside one of its blocks", wider))
		}

		list = inner
	}
}

// enclosingFunc is the top-level function whose braces hold the range on
// lines of their own.
func enclosingFunc(pf *parsedFile, want lineSpan) *ast.FuncDecl {
	for _, node := range pf.ast.Decls {
		fn, ok := node.(*ast.FuncDecl)
		if ok && fn.Body != nil && pf.line(fn.Body.Lbrace) < want.from && want.to < pf.line(fn.Body.Rbrace) {
			return fn
		}
	}

	return nil
}

// innerList is the statement list of the innermost block or case clause
// inside the one statement the range overlaps that holds the whole range,
// or nil when the range overlaps more than one statement or no such block
// exists.
func innerList(pf *parsedFile, hit []ast.Stmt, want lineSpan) []ast.Stmt {
	if len(hit) != 1 {
		return nil
	}

	var (
		best      []ast.Stmt
		bestStart token.Pos
	)

	ast.Inspect(hit[0], func(n ast.Node) bool {
		list, interior, ok := blockInterior(pf, n)
		if ok && interior.covers(want) && n.Pos() > bestStart {
			best, bestStart = list, n.Pos()
		}

		return true
	})

	return best
}

// blockInterior is the statement list a node holds and the lines strictly
// between its opening and closing, for the nodes that hold one.
func blockInterior(pf *parsedFile, n ast.Node) ([]ast.Stmt, lineSpan, bool) {
	switch b := n.(type) {
	case *ast.BlockStmt:
		return b.List, lineSpan{from: pf.line(b.Lbrace) + 1, to: pf.line(b.Rbrace) - 1}, true
	case *ast.CaseClause:
		return b.Body, lineSpan{from: pf.line(b.Colon) + 1, to: pf.line(b.End())}, true
	case *ast.CommClause:
		return b.Body, lineSpan{from: pf.line(b.Colon) + 1, to: pf.line(b.End())}, true
	default:
		return nil, lineSpan{}, false
	}
}

// hazard names the first statement gopls would extract into code that
// compiles and behaves differently, or nothing. A defer would run when the
// new function returns, and gopls turns a break that leaves the range into a
// break of the switch it generates, which breaks nothing.
func hazard(pf *parsedFile, stmts []ast.Stmt) string {
	var found string

	for _, stmt := range stmts {
		walkUntil(stmt, 0, func(n ast.Node, breakable int) bool {
			switch n := n.(type) {
			case *ast.DeferStmt:
				found = fmt.Sprintf("line %d defers a call, which would then run when the extracted function "+
					"returns; leave the defer out of the range", pf.line(n.Pos()))
			case *ast.BranchStmt:
				if n.Tok == token.BREAK && n.Label == nil && breakable == 0 {
					found = fmt.Sprintf("line %d breaks out of a loop, switch, or select outside the range, "+
						"which gopls does not carry over; widen the range to include it, or label it", pf.line(n.Pos()))
				}
			}

			return found == ""
		})

		if found != "" {
			return found
		}
	}

	return ""
}

// walkUntil visits n and its descendants depth first, counting the loops,
// switches, and selects between n and each node that a bare break would
// leave. It skips function literals, whose statements run on their own
// terms, and stops once visit returns false.
func walkUntil(n ast.Node, breakable int, visit func(n ast.Node, breakable int) bool) bool {
	if !visit(n, breakable) {
		return false
	}

	switch n.(type) {
	case *ast.FuncLit:
		return true
	case *ast.ForStmt, *ast.RangeStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
		breakable++
	}

	keep := true

	ast.Inspect(n, func(child ast.Node) bool {
		if child == n || !keep {
			return keep
		}

		if child != nil {
			keep = walkUntil(child, breakable, visit)
		}

		return false
	})

	return keep
}

// mentions reports whether any identifier among the statements is name.
func mentions(stmts []ast.Stmt, name string) bool {
	found := false

	for _, stmt := range stmts {
		ast.Inspect(stmt, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && id.Name == name {
				found = true
			}

			return !found
		})
	}

	return found
}
