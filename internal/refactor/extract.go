package refactor

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"path"
	"strconv"
	"strings"
)

const (
	extractFunction = "refactor.extract.function"
	extractMethod   = "refactor.extract.method"
)

// ExtractResult describes an extraction that was applied.
type ExtractResult struct {
	// At is the file and line range that was extracted.
	At string
	// From is the function the lines were taken from.
	From string
	// Signature is the new function's declaration up to its body.
	Signature string
	// StillBroken counts the compile errors the package had before the
	// extraction and still has.
	StillBroken int
	// DryRun is set when the tree was put back after the extraction ran.
	DryRun bool
}

// Summary is the short report of an extraction. The signature is in it
// because gopls chose the parameters and results, and the caller decides
// whether they read well.
func (r ExtractResult) Summary() string {
	out := fmt.Sprintf("extracted %s from %s into %s\n", r.At, r.From, r.Signature) + alreadyBroken(r.StillBroken)
	if r.DryRun {
		return dryRunHeader + out
	}

	return out
}

// Extract moves the statements on a line range of a function into a new
// function named newName, leaving a call in their place. The parameters
// and results are what gopls works out. Inside a method whose receiver the lines
// use, the new function is a method on the same receiver.
//
// The range is spec's path:FROM-TO, or path:LINE, and must select whole
// statements of one block. It refuses a range that cuts a statement, one
// holding a defer or a break out of the range, which gopls would extract
// into code that compiles and behaves differently, a name gopls rejects,
// and a result that adds a compile error.
func (r *Refactorer) Extract(ctx context.Context, spec, newName string) (ExtractResult, error) {
	if !token.IsIdentifier(newName) || newName == "_" {
		return ExtractResult{}, &InvalidNameError{Name: newName, Reason: "it is a keyword or not a Go identifier"}
	}

	s, err := r.open()
	if err != nil {
		return ExtractResult{}, err
	}
	defer s.close()

	pf, want, err := s.lineTarget(spec)
	if err != nil {
		return ExtractResult{}, err
	}

	p, err := pickStatements(pf, want)
	if err != nil {
		return ExtractResult{}, err
	}

	if reason := hazard(pf, p.stmts); reason != "" {
		return ExtractResult{}, extractRefusal(pf, want, reason)
	}

	res := ExtractResult{At: pf.rel + ":" + want.String(), From: funcDeclaration(pf, p.fn).name}

	if res.Signature, err = s.applyExtract(ctx, pf, p, newName); err != nil {
		return ExtractResult{}, s.fail(err)
	}

	if res.StillBroken, err = s.verify(ctx, []string{pf.rel}); err != nil {
		var build *BuildError
		if errors.As(err, &build) {
			err = fmt.Errorf("%w\ngopls extracted these lines into code that does not compile, a gopls bug; "+
				"try a different range, or edit by hand", err)
		}

		return ExtractResult{}, err
	}

	res.DryRun, err = r.finish(s)

	return res, err
}

// lineTarget reads a path:FROM-TO or path:LINE argument.
func (s *session) lineTarget(spec string) (*parsedFile, lineSpan, error) {
	bad := func(reason string) error { return &TargetError{Target: spec, Reason: reason} }

	i := strings.LastIndexByte(spec, ':')
	if i <= 0 {
		return nil, lineSpan{}, bad("want path/to/file.go:FROM-TO, the first and last line to extract")
	}

	want, ok := parseLines(spec[i+1:])
	if !ok {
		return nil, lineSpan{}, bad("want FROM-TO after the colon, two line numbers with the first no larger")
	}

	rel, ok := s.relPath(spec[:i])
	if !ok || !strings.HasSuffix(rel, goExt) {
		return nil, lineSpan{}, bad("the path must be a .go file inside the module")
	}

	if found, isDir := s.tree.exists(rel); !found || isDir {
		return nil, lineSpan{}, bad("no such file: " + rel)
	}

	pf, err := s.tree.parse(rel)
	if err != nil {
		return nil, lineSpan{}, err
	}

	if last := pf.tok.LineCount(); want.to > last {
		return nil, lineSpan{}, bad(fmt.Sprintf("%s has %d lines", rel, last))
	}

	return pf, want, nil
}

func parseLines(text string) (lineSpan, bool) {
	from, to, ranged := strings.Cut(text, "-")
	if !ranged {
		to = from
	}

	a, errA := strconv.Atoi(strings.TrimSpace(from))
	b, errB := strconv.Atoi(strings.TrimSpace(to))

	return lineSpan{from: a, to: b}, errA == nil && errB == nil && a >= 1 && a <= b
}

// applyExtract has gopls extract the statements, renames what it named the
// result to newName, and returns the new declaration's signature.
func (s *session) applyExtract(ctx context.Context, pf *parsedFile, p *picked, newName string) (string, error) {
	if err := s.keepDir(path.Dir(pf.rel)); err != nil {
		return "", err
	}

	before := map[string]bool{}
	for _, d := range declarationsOf(pf) {
		before[d.name] = true
	}

	kind := extractFunction
	if recv := receiverIdent(p.fn); recv != "" && mentions(p.stmts, recv) {
		kind = extractMethod
	}

	at := fmt.Sprintf("%s:#%d-#%d", pf.rel, p.start(pf), p.end(pf))
	if err := s.tools.codeAction(ctx, "extraction", kind, at); err != nil {
		return "", err
	}

	delete(s.tree.cache, pf.rel)

	made, err := s.newFunc(pf.rel, before)
	if err != nil {
		return "", err
	}

	if made.base() != newName {
		if _, err := s.tools.rename(ctx, position(made.file.rel, made.ident), newName); err != nil {
			return "", err
		}

		delete(s.tree.cache, pf.rel)

		if made, err = s.newFunc(pf.rel, before); err != nil {
			return "", err
		}
	}

	return signature(&made), nil
}

// signature is a function declaration's text up to its body, on one line.
func signature(d *declaration) string {
	fn, ok := d.node.(*ast.FuncDecl)
	if !ok || fn.Body == nil {
		return d.name
	}

	text := d.file.src[d.file.offset(fn.Pos()):d.file.offset(fn.Body.Lbrace)]

	return strings.Join(strings.Fields(string(text)), " ")
}

// newFunc finds the function declaration a file gained.
func (s *session) newFunc(rel string, before map[string]bool) (declaration, error) {
	pf, err := s.tree.parse(rel)
	if err != nil {
		return declaration{}, err
	}

	for _, d := range declarationsOf(pf) {
		if (d.kind == kindFunc || d.kind == kindMethod) && !before[d.name] {
			return d, nil
		}
	}

	return declaration{}, &GoplsError{Op: "extraction", Reason: "it reported success and added no function to " + rel}
}

// keepDir records every Go file of a directory before gopls writes there.
func (s *session) keepDir(dir string) error {
	files, err := s.tree.goFilesIn(dir)
	if err != nil {
		return err
	}

	for _, file := range files {
		if err := s.snap.keep(file); err != nil {
			return err
		}
	}

	return nil
}

// receiverIdent is the name a method calls its receiver, or nothing for a
// function or an unnamed receiver.
func receiverIdent(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 || len(fn.Recv.List[0].Names) == 0 {
		return ""
	}

	if name := fn.Recv.List[0].Names[0].Name; name != "_" {
		return name
	}

	return ""
}
