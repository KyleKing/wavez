package refactor

import (
	"fmt"
	"strings"
)

// RootError reports that no go.mod was found at or above a directory.
type RootError struct {
	Dir string
}

func (e *RootError) Error() string {
	return fmt.Sprintf("no go.mod at or above %s; run inside a Go module or pass --root DIR", e.Dir)
}

// TargetError reports a target that cannot be read as path:Name.
type TargetError struct {
	Target string
	Reason string
}

func (e *TargetError) Error() string {
	return fmt.Sprintf("bad target %q: %s", e.Target, e.Reason)
}

// InvalidNameError reports a new name gopls would refuse before asking it.
type InvalidNameError struct {
	Name   string
	Reason string
}

func (e *InvalidNameError) Error() string {
	return fmt.Sprintf("cannot rename to %q: %s", e.Name, e.Reason)
}

// NotFoundError reports a name that the target file or package does not
// declare, with what the caller most likely meant.
type NotFoundError struct {
	Name  string
	Scope string
	// Elsewhere is the target that does declare the name, when a sibling
	// file of the one searched holds it.
	Elsewhere string
	Near      []string
}

func (e *NotFoundError) Error() string {
	msg := fmt.Sprintf("no declaration named %q in %s", e.Name, e.Scope)

	switch {
	case e.Elsewhere != "":
		return msg + "; it is declared in another file of the package, so target " + e.Elsewhere
	case len(e.Near) > 0:
		return msg + "; closest names there: " + strings.Join(e.Near, ", ")
	default:
		return msg + "; target the file that declares it, or the package directory to search every file"
	}
}

// Candidate is one declaration an ambiguous name could mean.
type Candidate struct {
	// Target is the spelling that selects this declaration alone.
	Target string
	Kind   string
	Line   int
}

// AmbiguousError reports a name that more than one declaration answers to.
type AmbiguousError struct {
	Name       string
	Candidates []Candidate
}

func (e *AmbiguousError) Error() string {
	var b strings.Builder

	fmt.Fprintf(&b, "%q names %d declarations; use one of these targets instead:", e.Name, len(e.Candidates))

	for _, c := range e.Candidates {
		fmt.Fprintf(&b, "\n  %s  (%s, line %d)", c.Target, c.Kind, c.Line)
	}

	return b.String()
}

// GroupedError reports a name declared together with others in one const,
// var, or type declaration, which move and delete only take whole.
type GroupedError struct {
	Name     string
	At       string
	Siblings []string
}

func (e *GroupedError) Error() string {
	return fmt.Sprintf("%s is declared in one block with %s at %s; name all of them in the same call, "+
		"or split the block by hand first", e.Name, strings.Join(e.Siblings, ", "), e.At)
}

// ToolMissingError reports a program the refactor shells out to that is not
// installed.
type ToolMissingError struct {
	Program string
}

func (e *ToolMissingError) Error() string {
	if strings.Contains(e.Program, "gopls") {
		return e.Program + " is not installed or not on PATH; install it with " +
			"`go install golang.org/x/tools/gopls@latest` and make sure $(go env GOPATH)/bin is on PATH"
	}

	return e.Program + " is not installed or not on PATH"
}

// GoplsError reports that gopls refused or failed an operation, carrying its
// own reason: a keyword, a conflicting name, a symbol in a dependency.
type GoplsError struct {
	Op     string
	Reason string
}

func (e *GoplsError) Error() string {
	return fmt.Sprintf("gopls refused the %s, nothing was changed: %s", e.Op, e.Reason)
}

// BuildError reports that an operation broke the build of the packages it
// touched, with the compile errors it added. The files are back as they
// were.
type BuildError struct {
	Output string
}

func (e *BuildError) Error() string {
	return "the result does not build, so every file was restored (line numbers are the attempted result's):\n" +
		e.Output
}

// CrossPackageError reports a move whose destination is another package.
type CrossPackageError struct {
	From string
	To   string
}

func (e *CrossPackageError) Error() string {
	return fmt.Sprintf("move stays inside one package: the source is %s and the destination is %s. "+
		"Moving between packages is not supported, because it changes every qualified reference; "+
		"pick a destination file in the same directory and package", e.From, e.To)
}

// Holder is one use of a declaration that a delete would leave dangling.
type Holder struct {
	// Name is the declaration being deleted that the holder uses.
	Name string
	File string
	// In is the declaration enclosing the use, empty when the use is not
	// inside one.
	In   string
	Line int
}

// StillUsedError reports declarations that something outside the set being
// deleted still references.
type StillUsedError struct {
	// Retry is the command line that deletes the holders too.
	Retry   string
	Holders []Holder
}

const maxHolderLines = 15

func (e *StillUsedError) Error() string {
	var b strings.Builder

	b.WriteString("still used outside the declarations being deleted, so nothing was deleted:")

	for i, h := range e.Holders {
		if i == maxHolderLines {
			fmt.Fprintf(&b, "\n  (+%d more)", len(e.Holders)-maxHolderLines)

			break
		}

		fmt.Fprintf(&b, "\n  %s used at %s:%d", h.Name, h.File, h.Line)

		if h.In != "" {
			b.WriteString(" in " + h.In)
		}
	}

	if e.Retry != "" {
		b.WriteString("\nif those holders should go too (its tests, say), run:\n  " + e.Retry +
			"\notherwise change them first")
	}

	return b.String()
}
