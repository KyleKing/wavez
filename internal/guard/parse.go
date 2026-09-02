package guard

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// This file reads a shell command through mvdan.cc/sh's bash parser and
// flattens the tree into the shape the rest of the package already expects:
// sequence fragments, pipeline stages as word lists, and every command and
// process substitution at any depth. Spans are always taken from the original
// text, so a fragment shown in a refusal is the exact text that was written
// and a verdict stays a pure function of the command.
//
// Text the parser rejects is answered by the hand-rolled splitters in
// split.go, which stay for exactly that: a verdict may never depend on the
// parse succeeding.

// cmdNode is one shell command as the rules read it: its fragment of the
// original line, and its words as the shell lexes them, with redirect
// operator tokens embedded where they stood.
type cmdNode struct {
	fragment string
	words    []string
}

// pipeline is one sequence element, split into the stages a pipe separates.
type pipeline struct {
	fragment string
	stages   []cmdNode
}

// subNode is one substitution: the fragment it ran and the sequence elements
// inside it, parsed on their own so nesting is reached at every depth.
type subNode struct {
	fragment string
	top      []pipeline
}

// parsed is the whole reading of one command line.
type parsed struct {
	top  []pipeline
	subs []subNode
}

// walk parses command as bash and walks its tree. It returns nil when the
// parser rejected the text, and the fallbacks in split.go answer for it.
func walk(command string) *parsed {
	file, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return nil
	}

	w := &walker{src: command}
	for _, stmt := range file.Stmts {
		p := pipeline{fragment: w.stmtFragment(stmt)}
		w.stmtBody(stmt, &p)
		w.top = append(w.top, p)
	}
	w.walkSubs(file)

	return &parsed{top: w.top, subs: w.subs}
}

// walker collects pipelines and substitutions as spans of src.
type walker struct {
	src  string
	top  []pipeline
	subs []subNode
}

// walkSubs records every command and process substitution in the file, at any
// depth: nested inside another substitution, inside a word, or inside an
// unquoted heredoc body, which the shell expands and so is code. A quoted
// heredoc delimiter suppresses expansion, and the parser keeps its body
// literal, so nothing inside it is walked.
func (w *walker) walkSubs(node syntax.Node) {
	syntax.Walk(node, func(n syntax.Node) bool {
		switch node := n.(type) {
		case *syntax.CmdSubst:
			w.addSub(w.fragmentOf(node.Left, node.Right, 1), node.Stmts)
		case *syntax.ProcSubst:
			w.addSub(w.fragmentOf(node.OpPos, node.Rparen, 1), node.Stmts)
		}

		return true
	})
}

// fragmentOf is the source text a node covers, with trailing width extra
// bytes for a closing delimiter.
func (w *walker) fragmentOf(start, end syntax.Pos, width int) string {
	s, e := w.span(start, end)
	e += width
	if e > len(w.src) {
		e = len(w.src)
	}

	return w.src[s:e]
}

// stmtBody flattens one statement into p. A pipeline is a BinaryCmd tree of
// |, &&, and ||; each of its ends is either another BinaryCmd or a single
// command.
func (w *walker) stmtBody(stmt *syntax.Stmt, p *pipeline) {
	if stmt == nil || stmt.Cmd == nil {
		return
	}

	if bin, ok := stmt.Cmd.(*syntax.BinaryCmd); ok {
		w.stmtBody(bin.X, p)
		w.stmtBody(bin.Y, p)

		return
	}

	if call, ok := stmt.Cmd.(*syntax.CallExpr); ok {
		p.stages = append(p.stages, cmdNode{
			fragment: w.stmtFragment(stmt),
			words:    w.callWords(call, stmt),
		})

		return
	}

	// A compound command (if, for, while, case, braces, subshell, function
	// body) runs the statements inside it, so those are walked as commands
	// of this same pipeline.
	w.compoundStmts(stmt.Cmd, p)
}

// compoundStmts walks the statements a compound command would run, so each
// one is judged as a command of this same pipeline.
func (w *walker) compoundStmts(cmd syntax.Command, p *pipeline) {
	syntax.Walk(cmd, func(n syntax.Node) bool {
		if stmt, ok := n.(*syntax.Stmt); ok {
			w.stmtBody(stmt, p)
		}

		return true
	})
}

// addSub records one substitution and the pipelines inside it.
func (w *walker) addSub(fragment string, stmts []*syntax.Stmt) {
	sub := subNode{fragment: fragment}
	for _, st := range stmts {
		p := pipeline{fragment: w.stmtFragment(st)}
		w.stmtBody(st, &p)
		sub.top = append(sub.top, p)
	}

	w.subs = append(w.subs, sub)
}

// callWords renders a statement's words in source order: arguments, then its
// redirects spelled as an fd prefix plus operator and then their targets,
// with a backgrounding or sequencing operator left out.
func (w *walker) callWords(call *syntax.CallExpr, stmt *syntax.Stmt) []string {
	type token struct {
		word string
		off  int
	}

	tokens := make([]token, 0, len(call.Args)+2*len(stmt.Redirs))

	for _, word := range call.Args {
		tokens = append(tokens, token{off: int(word.Pos().Offset()), word: w.word(word)})
	}

	for _, redir := range stmt.Redirs {
		op := redir.Op.String()
		if redir.N != nil {
			op = redir.N.Value + op
		}

		tokens = append(tokens, token{off: int(redir.OpPos.Offset()), word: op})

		if redir.Word != nil {
			tokens = append(tokens, token{
				off:  int(redir.Word.Pos().Offset()),
				word: w.word(redir.Word),
			})
		}
	}

	// insertion sort: the lists are short and each is nearly sorted already
	for i := 1; i < len(tokens); i++ {
		for j := i; j > 0 && tokens[j].off < tokens[j-1].off; j-- {
			tokens[j], tokens[j-1] = tokens[j-1], tokens[j]
		}
	}

	words := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		if tok.word != "" {
			words = append(words, tok.word)
		}
	}

	return words
}

// word renders one shell word from its parts. Quoting is stripped the way
// the old tokenizer stripped it; parameters, substitutions, and arithmetic
// keep their own spelling, since the rules match on the text as written.
func (w *walker) word(word *syntax.Word) string {
	var b strings.Builder

	for _, part := range word.Parts {
		switch p := part.(type) {
		case *syntax.Lit:
			b.WriteString(p.Value)
		case *syntax.SglQuoted:
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			for _, inner := range p.Parts {
				b.WriteString(w.part(inner))
			}
		default:
			b.WriteString(w.part(part))
		}
	}

	return b.String()
}

// part renders one non-literal word part by its source span, which keeps the
// spelling of a parameter, substitution, glob, or expansion exactly as
// written.
func (w *walker) part(part syntax.WordPart) string {
	start, end := w.span(part.Pos(), part.End())

	return w.src[start:end]
}

// stmtFragment is stmt's own text: from its first token to the end of its
// last, with a trailing operator and surrounding whitespace dropped.
func (w *walker) stmtFragment(stmt *syntax.Stmt) string {
	start, end := w.span(stmt.Position, stmt.End())

	return strings.TrimSpace(w.src[start:end])
}

// span converts two positions into a span of w.src. The end position names
// the first byte past the node, except where width extends it, and both ends
// are clamped to the text.
func (w *walker) span(start, end syntax.Pos, width ...int) (int, int) {
	s := int(start.Offset())
	e := int(end.Offset())
	if len(width) > 0 {
		e += width[0]
	}

	if s < 0 {
		s = 0
	}

	if e > len(w.src) {
		e = len(w.src)
	}

	if s > e {
		s = e
	}

	return s, e
}
