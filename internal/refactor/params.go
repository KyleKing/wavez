package refactor

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// ParamsResult describes a parameter list change that was applied.
type ParamsResult struct {
	Name string
	// Old and New are the parameter lists before and after, in parentheses.
	Old, New string
	// Files lists the files rewritten, relative to the module root.
	Files []string
	// Excluded lists lines in files the build leaves out that may call the
	// function. gopls does not load those, so they are not rewritten.
	Excluded []Mention
	// StillBroken counts the compile errors the touched packages had
	// before the change and still have.
	StillBroken int
	// ExcludedUnchecked is set when the go tool could not say which files
	// the build holds.
	ExcludedUnchecked bool
	// DryRun is set when the tree was put back after the change ran.
	DryRun bool
}

// Summary is the short report of a parameter list change.
func (r ParamsResult) Summary() string {
	var b strings.Builder

	if r.DryRun {
		b.WriteString(dryRunHeader)
	}

	fmt.Fprintf(&b, "changed %s%s to %s%s in %s: %s\n", r.Name, r.Old, r.Name, r.New,
		count(len(r.Files), "file"), capped(r.Files, maxFileNames))
	b.WriteString(alreadyBroken(r.StillBroken))

	if r.ExcludedUnchecked {
		b.WriteString("go list failed, so files excluded from this build were not checked for calls\n")
	}

	listed(&b, r.Excluded, "not rewritten, excluded from this build (GOOS/GOARCH or build tag); "+
		"these may call it with the old parameters:\n")

	return b.String()
}

// param is one parameter of a function as gopls names it: a blank or
// unnamed one is _0, _1, and so on in order.
type param struct {
	name     string
	typ      string
	variadic bool
}

// Params rewrites a function's parameter list to the named parameters in
// the given order, and every call in the module to match. Leaving a name
// out removes that parameter, and a new order moves the arguments of each
// call with it. The target is path:Func or path:Type.Method; names is a
// comma-separated list, empty to remove every parameter.
//
// Adding a parameter is not supported, because gopls cannot. It refuses, changing
// nothing, an unknown or repeated name, a removed parameter the body still
// uses, a removed argument that holds a call (dropping it would drop the
// call's effect), a function used as a value, and a change that adds a
// compile error, which is what changing an interface method's
// implementation alone does.
func (r *Refactorer) Params(ctx context.Context, spec, names string) (ParamsResult, error) {
	s, err := r.open()
	if err != nil {
		return ParamsResult{}, err
	}
	defer s.close()

	decl, fn, err := s.paramsTarget(spec)
	if err != nil {
		return ParamsResult{}, err
	}

	params := paramsOf(decl.file, fn)

	order, err := paramOrder(decl.name, params, names)
	if err != nil {
		return ParamsResult{}, err
	}

	if err := s.droppedCalls(ctx, &decl, params, order); err != nil {
		return ParamsResult{}, err
	}

	files, err := s.keepModule()
	if err != nil {
		return ParamsResult{}, err
	}

	res := ParamsResult{Name: decl.name, Old: paramList(params, nil), New: paramList(params, order)}

	if res.Files, err = s.applyParams(ctx, &decl, fn, params, order); err != nil {
		return ParamsResult{}, s.fail(err)
	}

	if res.StillBroken, err = s.verify(ctx, res.Files); err != nil {
		return ParamsResult{}, err
	}

	excluded, checked := s.excluded(ctx, files, &decl)
	res.Excluded, res.ExcludedUnchecked = excluded, !checked
	res.DryRun, err = r.finish(s)

	return res, err
}

func (s *session) paramsTarget(spec string) (declaration, *ast.FuncDecl, error) {
	t, err := s.parseTarget(spec)
	if err != nil {
		return declaration{}, nil, err
	}

	if len(t.names) != 1 {
		return declaration{}, nil, &TargetError{Target: spec, Reason: "params takes exactly one function"}
	}

	decl, err := s.resolve(t, t.names[0], true)
	if err != nil {
		return declaration{}, nil, err
	}

	fn, ok := decl.node.(*ast.FuncDecl)

	switch {
	case decl.isInterfaceMethod():
		return declaration{}, nil, &ParamsError{Func: decl.name, Reason: "it is an interface method, and gopls " +
			"changes one function at a time; change the interface and its implementations by hand"}
	case !ok:
		return declaration{}, nil, &ParamsError{Func: decl.name, Reason: "it is a " + decl.kind.String() +
			", not a function or method"}
	}

	return decl, fn, nil
}

func paramsOf(pf *parsedFile, fn *ast.FuncDecl) []param {
	taken := map[string]bool{}

	for _, field := range fn.Type.Params.List {
		for _, name := range field.Names {
			taken[name.Name] = true
		}
	}

	var (
		out    []param
		blanks int
	)

	blank := func() string {
		for {
			name := "_" + strconv.Itoa(blanks)
			blanks++

			if !taken[name] {
				return name
			}
		}
	}

	for _, field := range fn.Type.Params.List {
		_, variadic := field.Type.(*ast.Ellipsis)
		p := param{typ: string(pf.src[pf.offset(field.Type.Pos()):pf.offset(field.Type.End())]), variadic: variadic}

		if len(field.Names) == 0 {
			p.name = blank()
			out = append(out, p)
		}

		for _, name := range field.Names {
			p.name = name.Name
			if p.name == "_" {
				p.name = blank()
			}

			out = append(out, p)
		}
	}

	return out
}

// paramOrder reads the wanted names as indexes into params.
func paramOrder(fn string, params []param, names string) ([]int, error) {
	known := make([]string, 0, len(params))
	for _, p := range params {
		known = append(known, p.name)
	}

	refuse := func(reason string) error { return &ParamsError{Func: fn, Reason: reason} }

	var order []int

	for name := range strings.SplitSeq(names, ",") {
		if name = strings.TrimSpace(name); name == "" {
			continue
		}

		i := slices.Index(known, name)

		switch {
		case i < 0:
			return nil, refuse(fmt.Sprintf("it has no parameter %q; its parameters are %s "+
				"(gopls calls unnamed ones _0, _1, ...), and adding one is not supported", name, list(known)))
		case slices.Contains(order, i):
			return nil, refuse(name + " is named twice")
		}

		order = append(order, i)
	}

	if slices.Equal(order, identity(len(params))) {
		return nil, refuse("that is already its parameter list")
	}

	if v := len(params) - 1; v >= 0 && params[v].variadic && slices.Contains(order[:max(len(order)-1, 0)], v) {
		return nil, refuse(params[v].name + " is variadic, so it has to stay last")
	}

	return order, nil
}

func identity(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}

	return out
}

func list(names []string) string {
	if len(names) == 0 {
		return "none"
	}

	return strings.Join(names, ", ")
}

// paramList spells the parameters an order selects, all of them when the
// order is nil.
func paramList(params []param, order []int) string {
	if order == nil {
		order = identity(len(params))
	}

	parts := make([]string, 0, len(order))
	for _, i := range order {
		parts = append(parts, params[i].name+" "+params[i].typ)
	}

	return "(" + strings.Join(parts, ", ") + ")"
}

// applyParams asks gopls for the new signature, by renaming the func
// keyword to it, which is how its command line reaches the change, and
// returns the files it rewrote.
func (s *session) applyParams(
	ctx context.Context, decl *declaration, fn *ast.FuncDecl, params []param, order []int,
) ([]string, error) {
	pf := decl.file

	sig := "func" + paramList(params, order)
	if fn.Type.Results != nil {
		sig += " " + string(pf.src[pf.offset(fn.Type.Results.Pos()):pf.offset(fn.Type.Results.End())])
	}

	files, err := s.tools.rename(ctx, position(pf.rel, pf.offset(fn.Type.Func)), sig)
	if err == nil {
		slices.Sort(files)

		return files, nil
	}

	var refused *GoplsError
	if !errors.As(err, &refused) {
		return nil, err
	}

	return nil, explainParams(decl, fn, params, order, refused)
}

var (
	undefinedPattern = regexp.MustCompile(`undefined: (\w+)`)
	valuePattern     = regexp.MustCompile(`non-call function reference \{file://(\S+) (\d+):(\d+)`)
)

// explainParams turns the gopls refusals a caller can act on into what to
// do about them, and passes the rest through.
func explainParams(decl *declaration, fn *ast.FuncDecl, params []param, order []int, refused *GoplsError) error {
	if m := undefinedPattern.FindStringSubmatch(refused.Reason); m != nil {
		i := slices.IndexFunc(params, func(p param) bool { return p.name == m[1] })
		if i >= 0 && !slices.Contains(order, i) {
			lines := usedLines(decl.file, fn.Body, m[1])

			return &ParamsError{Func: decl.name, Reason: fmt.Sprintf("its body still uses %s on %s %s; "+
				"remove those uses first, or keep the parameter",
				m[1], plural(len(lines), "line"), strings.Join(lines, ", "))}
		}
	}

	if m := valuePattern.FindStringSubmatch(refused.Reason); m != nil {
		// gopls prints this position as LSP does, counting from zero.
		row, _ := strconv.Atoi(m[2]) //nolint:errcheck // the pattern matched digits
		col, _ := strconv.Atoi(m[3]) //nolint:errcheck // the pattern matched digits

		return &ParamsError{Func: decl.name, Reason: fmt.Sprintf("it is used as a value at %s:%d:%d, not called, "+
			"so that use cannot be rewritten; wrap it in a func literal there first", m[1], row+1, col+1)}
	}

	refused.Op = "signature change"

	return refused
}

func plural(n int, noun string) string {
	if n == 1 {
		return noun
	}

	return noun + "s"
}

func usedLines(pf *parsedFile, body *ast.BlockStmt, name string) []string {
	var lines []string

	ast.Inspect(body, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == name {
			if line := strconv.Itoa(pf.line(id.Pos())); !slices.Contains(lines, line) {
				lines = append(lines, line)
			}
		}

		return true
	})

	return lines
}

// maxDroppedLines bounds how many calls a refusal lists.
const maxDroppedLines = 10

// droppedCalls refuses a removal that would drop a call written as an
// argument of the removed parameter: gopls deletes the argument, and with
// it whatever the call did.
func (s *session) droppedCalls(ctx context.Context, decl *declaration, params []param, order []int) error {
	if len(order) == len(params) {
		return nil
	}

	refs, err := s.tools.references(ctx, position(decl.file.rel, decl.ident), false)
	if err != nil {
		return err
	}

	var found []string

	for _, loc := range refs {
		found = append(found, s.droppedAt(loc, decl.base(), params, order)...)
	}

	if len(found) == 0 {
		return nil
	}

	if extra := len(found) - maxDroppedLines; extra > 0 {
		found = append(found[:maxDroppedLines], fmt.Sprintf("(+%d more)", extra))
	}

	return &ParamsError{Func: decl.name, Reason: "removing an argument that holds a call would drop the call; " +
		"move each to its own statement first:\n  " + strings.Join(found, "\n  ")}
}

// droppedAt lists the removed arguments holding a call in the call whose
// callee sits at a location.
func (s *session) droppedAt(loc location, name string, params []param, order []int) []string {
	pf, err := s.tree.parse(loc.file)
	if err != nil {
		return nil
	}

	at, ok := loc.offset(pf.src, name)
	if !ok {
		return nil
	}

	var found []string

	ast.Inspect(pf.ast, func(n ast.Node) bool {
		call, isCall := n.(*ast.CallExpr)
		if !isCall || calleeOffset(pf, call.Fun) != at {
			return true
		}

		for i, arg := range call.Args {
			if slices.Contains(order, min(i, len(params)-1)) || !hasCall(arg) {
				continue
			}

			found = append(found, fmt.Sprintf("%s:%d: %s", pf.rel, pf.line(arg.Pos()),
				pf.src[pf.offset(arg.Pos()):pf.offset(arg.End())]))
		}

		return false
	})

	return found
}

// calleeOffset is the offset of the name a call expression calls, or -1.
func calleeOffset(pf *parsedFile, fun ast.Expr) int {
	switch f := fun.(type) {
	case *ast.Ident:
		return pf.offset(f.Pos())
	case *ast.SelectorExpr:
		return pf.offset(f.Sel.Pos())
	case *ast.IndexExpr:
		return calleeOffset(pf, f.X)
	case *ast.IndexListExpr:
		return calleeOffset(pf, f.X)
	case *ast.ParenExpr:
		return calleeOffset(pf, f.X)
	default:
		return -1
	}
}

// pureCalls are the builtins and predeclared types whose call has no effect
// beyond its value.
var pureCalls = map[string]bool{
	"len": true, "cap": true, "min": true, "max": true, "new": true, "make": true,
	"complex": true, "real": true, "imag": true, "string": true, "bool": true, "byte": true, "rune": true,
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true, "uintptr": true,
	"float32": true, "float64": true, "complex64": true, "complex128": true, "any": true, "error": true,
}

// hasCall reports whether an expression calls something other than a
// builtin or a conversion to a type spelled as a literal, or receives from
// a channel. Syntax cannot tell a conversion to a named type from a call,
// so that counts as a call.
func hasCall(expr ast.Expr) bool {
	found := false

	ast.Inspect(expr, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.CallExpr:
			found = found || !isConversion(n.Fun)
		case *ast.UnaryExpr:
			found = found || n.Op == token.ARROW
		}

		return !found
	})

	return found
}

func isConversion(fun ast.Expr) bool {
	switch f := fun.(type) {
	case *ast.Ident:
		return pureCalls[f.Name]
	case *ast.ParenExpr:
		return isConversion(f.X)
	case *ast.ArrayType, *ast.MapType, *ast.ChanType, *ast.FuncType, *ast.InterfaceType, *ast.StarExpr:
		return true
	default:
		return false
	}
}
