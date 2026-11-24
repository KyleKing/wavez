package refactor

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/format"
	"path"
)

const inlineCall = "refactor.inline.call"

// InlineResult describes an inlining that was applied.
type InlineResult struct {
	At   string
	Name string
	// Calls counts the calls replaced by the callee's body.
	Calls int
	// StillBroken counts the compile errors the package had before and
	// still has.
	StillBroken int
	// DryRun is set when the tree was put back after the inlining ran.
	DryRun bool
}

// Summary is the short report of an inlining.
func (r InlineResult) Summary() string {
	out := fmt.Sprintf("inlined %s to %s on %s\n", count(r.Calls, "call"), r.Name, r.At) + alreadyBroken(r.StillBroken)
	if r.DryRun {
		return dryRunHeader + out
	}

	return out
}

// Inline replaces each call to the function or method called name on a
// line range with the callee's body, as gopls's inliner writes it: the
// arguments are bound so each is evaluated once and in order. The
// declaration stays. It refuses a range with no such call, a callee gopls
// will not inline, such as one whose body uses names unexported to the
// caller, and a result that adds a compile error.
func (r *Refactorer) Inline(ctx context.Context, spec, name string) (InlineResult, error) {
	s, err := r.open()
	if err != nil {
		return InlineResult{}, err
	}
	defer s.close()

	pf, want, err := s.lineTarget(spec)
	if err != nil {
		return InlineResult{}, err
	}

	res := InlineResult{At: pf.rel + ":" + want.String(), Name: name, Calls: len(callsTo(pf, want, name))}
	if res.Calls == 0 {
		return InlineResult{}, &InlineError{At: res.At, Reason: "no call to " + name + " there; name the function " +
			"as it is called, without its package or receiver"}
	}

	if res.Calls, err = s.applyInline(ctx, pf.rel, want, name, res.Calls); err != nil {
		return InlineResult{}, s.fail(err)
	}

	if res.StillBroken, err = s.verify(ctx, []string{pf.rel}); err != nil {
		return InlineResult{}, err
	}

	res.DryRun, err = r.finish(s)

	return res, err
}

// applyInline inlines the first remaining call on the range, at most
// calls times, since gopls runs one code action per process, and returns
// how many it inlined. Counting the calls first keeps a callee that calls
// itself from inlining forever. The file is formatted afterwards when it
// was formatted before, because the inliner leaves its output as printed.
func (s *session) applyInline(ctx context.Context, rel string, want lineSpan, name string, calls int) (int, error) {
	if err := s.keepDir(path.Dir(rel)); err != nil {
		return 0, err
	}

	done, lines := 0, 0

	for ; done < calls; done++ {
		delete(s.tree.cache, rel)

		pf, err := s.tree.parse(rel)
		if err != nil {
			return 0, err
		}

		// An inlined body spans more lines than its call, which moves the
		// calls after it down.
		if lines > 0 {
			want.to += pf.tok.LineCount() - lines
		}

		lines = pf.tok.LineCount()

		at := callsTo(pf, want, name)
		if len(at) == 0 {
			break
		}

		if err := s.tools.codeAction(ctx, "inlining", inlineCall, position(rel, at[0])); err != nil {
			return 0, err
		}
	}

	delete(s.tree.cache, rel)

	return done, s.formatAgain(rel)
}

// formatAgain formats a file that was formatted before the operation.
func (s *session) formatAgain(rel string) error {
	src, err := s.tree.read(rel)
	if err != nil {
		return err
	}

	if before, _ := s.snap.original(rel); !isFormatted(before) {
		return nil
	}

	// A result that does not parse is left for the build check to report.
	if formatted, fmtErr := format.Source(src); fmtErr == nil && !bytes.Equal(formatted, src) {
		return s.snap.write(rel, formatted)
	}

	return nil
}

// callsTo lists the offsets of the callee names of the calls to name that
// start on the line range.
func callsTo(pf *parsedFile, want lineSpan, name string) []int {
	var out []int

	ast.Inspect(pf.ast, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !want.covers(lineSpan{from: pf.line(call.Pos()), to: pf.line(call.Pos())}) {
			return true
		}

		if at := calleeOffset(pf, call.Fun); at >= 0 && bytes.HasPrefix(pf.src[at:], []byte(name)) &&
			!isIdentByte(pf.src, at+len(name)) {
			out = append(out, at)
		}

		return true
	})

	return out
}

func isIdentByte(src []byte, at int) bool {
	if at >= len(src) {
		return false
	}

	c := src[at]

	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}
