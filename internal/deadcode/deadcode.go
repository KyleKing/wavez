// Package deadcode reports functions no path from a binary's main can
// reach, which is the question `go vet` and staticcheck's `unused` cannot
// answer for this project.
//
// Everything here lives under internal/, so every symbol is unexported API
// as far as the compiler is concerned and `unused` treats an exported one
// as reachable by definition. A test counts as a use, so an orphan with
// good tests looks alive. And a composition root that constructs a thing
// and stores it on a struct has used it, so an orphan one hop from main
// stays invisible. Reachability from main ignoring tests is the only check
// that catches that shape, and it caught five subsystems here.
package deadcode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"sort"
	"strings"
)

// Binary is the analyzer this package shells out to, the same one
// `go run golang.org/x/tools/cmd/deadcode@latest` installs.
const Binary = "deadcode"

// ErrUnavailable reports that the analyzer is not installed. Its message
// names how to get it, since a report that cannot run is otherwise
// indistinguishable from a clean one.
var ErrUnavailable = errors.New(
	"deadcode is not installed: `mise use -g go:golang.org/x/tools/cmd/deadcode` or " +
		"`go install golang.org/x/tools/cmd/deadcode@latest`",
)

// Func is one function no main reaches.
type Func struct {
	Package  string
	Name     string
	File     string
	AllowPat string
	Line     int
	Allowed  bool
}

// Report is one analysis over a package pattern.
type Report struct {
	// Unreached is every function the analyzer found, allowlisted or not,
	// so a caller can show the whole picture rather than only the part that
	// is actionable.
	Unreached []Func
}

// Actionable returns the functions no allowlist pattern covers: what is
// worth looking at rather than everything the analyzer found.
func (r Report) Actionable() []Func {
	out := make([]Func, 0, len(r.Unreached))

	for _, f := range r.Unreached {
		if !f.Allowed {
			out = append(out, f)
		}
	}

	return out
}

// Analyzer runs the reachability analysis over a project.
type Analyzer struct {
	root   string
	binary string
	allow  []string
}

// Option configures an Analyzer.
type Option func(*Analyzer)

// WithBinary overrides the analyzer executable resolved on PATH.
func WithBinary(name string) Option {
	return func(a *Analyzer) { a.binary = name }
}

// WithAllow sets the patterns that mark a function as deliberately
// unreachable from main. Each is matched against `package.Function` with
// path.Match, so `*.With*` covers functional options and `internal/x.*`
// covers a whole package.
func WithAllow(patterns []string) Option {
	return func(a *Analyzer) { a.allow = patterns }
}

// New builds an Analyzer over the module at root.
func New(root string, opts ...Option) *Analyzer {
	a := &Analyzer{root: root, binary: Binary}
	for _, opt := range opts {
		opt(a)
	}

	return a
}

// wirePackage is the analyzer's own JSON shape, whose keys are Go-exported
// names rather than snake_case, so the field tags mirror what it emits.
//
//nolint:tagliatelle // these names are the analyzer's wire format, not ours
type wirePackage struct {
	Path  string     `json:"Path"`
	Funcs []wireFunc `json:"Funcs"`
}

//nolint:tagliatelle // these names are the analyzer's wire format, not ours
type wireFunc struct {
	Name     string `json:"Name"`
	Position struct {
		File string `json:"File"`
		Line int    `json:"Line"`
	} `json:"Position"`
}

// Run analyzes every main package matching pattern and reports what none of
// them reach. A pattern matching no main package is an error rather than an
// empty report, because "nothing is unreachable" and "nothing was analyzed"
// must not read the same.
func (a *Analyzer) Run(ctx context.Context, pattern string) (Report, error) {
	if _, err := exec.LookPath(a.binary); err != nil {
		return Report{}, ErrUnavailable
	}

	//nolint:gosec // the binary and pattern are project configuration, never model output
	cmd := exec.CommandContext(ctx, a.binary, "-json", pattern)
	cmd.Dir = a.root

	out, err := cmd.Output()
	if err != nil {
		return Report{}, fmt.Errorf("running %s: %w%s", a.binary, err, stderrOf(err))
	}

	var pkgs []wirePackage
	if err := json.Unmarshal(out, &pkgs); err != nil {
		return Report{}, fmt.Errorf("parsing %s output: %w", a.binary, err)
	}

	return a.report(pkgs), nil
}

func (a *Analyzer) report(pkgs []wirePackage) Report {
	var report Report

	for _, p := range pkgs {
		for _, f := range p.Funcs {
			fn := Func{
				Package: p.Path,
				Name:    f.Name,
				File:    f.Position.File,
				Line:    f.Position.Line,
			}
			fn.AllowPat = a.matchAllow(p.Path, f.Name)
			fn.Allowed = fn.AllowPat != ""
			report.Unreached = append(report.Unreached, fn)
		}
	}

	sort.Slice(report.Unreached, func(i, j int) bool {
		if report.Unreached[i].File != report.Unreached[j].File {
			return report.Unreached[i].File < report.Unreached[j].File
		}

		return report.Unreached[i].Line < report.Unreached[j].Line
	})

	return report
}

// matchAllow returns the pattern that covers pkgPath.name, empty when none
// does. Both the full import path and its last segment are offered, so a
// pattern may name a package either way.
func (a *Analyzer) matchAllow(pkgPath, name string) string {
	subject := pkgPath + "." + name
	short := path.Base(pkgPath) + "." + name

	for _, pattern := range a.allow {
		for _, s := range []string{subject, short, name} {
			if ok, err := path.Match(pattern, s); err == nil && ok {
				return pattern
			}
		}
	}

	return ""
}

func stderrOf(err error) string {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
		return ": " + strings.TrimSpace(string(exitErr.Stderr))
	}

	return ""
}
