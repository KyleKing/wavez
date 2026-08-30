// Package finish holds the deterministic checks that answer the question
// the model reviewer was asked and answered badly: did this run do
// something of the right shape.
//
// Each check is a bound rather than a judgment, so a run that passes them
// all has not been declared correct. They exist because the reviewer as
// built objected to correct diffs (3 objections in 77 runs, both traceable
// ones on runs whose diff was right) and because a model judging a model
// wraps its own words around the same invention.
package finish

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/kyleking/wavez/internal/codeintel"
)

// Finding is one check failing, in the words a report needs.
type Finding struct {
	Check  string
	Detail string
}

// Report is what every check found. An empty report is a run that passed
// every bound, which is not the same as a run that did the task.
type Report struct {
	Findings []Finding
}

// OK reports whether every check held.
func (r Report) OK() bool { return len(r.Findings) == 0 }

// String renders the findings one per line.
func (r Report) String() string {
	lines := make([]string, 0, len(r.Findings))
	for _, f := range r.Findings {
		lines = append(lines, f.Check+": "+f.Detail)
	}

	return strings.Join(lines, "\n")
}

// Index is the symbol lookup the naming check needs. DeclaresName answers
// exactly, and Search is how a name that declares nothing is still looked
// for in the text, since a config key or a tool name is neither invented
// nor a symbol.
type Index interface {
	DeclaresName(ctx context.Context, name string) (bool, error)
	Search(ctx context.Context, q codeintel.SearchQuery) ([]codeintel.SearchResult, error)
}

// pathPattern matches a project-relative file path: at least one directory
// separator and an extension. A bare file name is not enough, since prose
// naming `truncate` should not be read as a path.
var pathPattern = regexp.MustCompile(`\b[\w.-]+(?:/[\w.-]+)+\.[A-Za-z]\w*\b`)

// symbolPattern matches an identifier the answer presents as code: a
// backticked token, optionally qualified and optionally called. Prose that
// names a symbol without marking it as one is not checked, because the
// alternative is guessing which English words were meant as identifiers.
var symbolPattern = regexp.MustCompile("`([A-Za-z_]\\w*(?:\\.[A-Za-z_]\\w*)*)(?:\\(\\))?`")

// NamedThingsExist reports every path and symbol the closing answer names
// that the tree and the index do not hold. It is exactly what `h1`
// invented: asked to name a file and a function, a run answered with both
// and neither existed.
func NamedThingsExist(ctx context.Context, root, answer string, index Index) (Report, error) {
	var report Report

	for _, path := range missingPaths(root, answer) {
		report.Findings = append(report.Findings, Finding{
			Check: "named path does not exist", Detail: path,
		})
	}

	missing, err := missingSymbols(ctx, answer, index)
	if err != nil {
		return Report{}, err
	}

	for _, name := range missing {
		report.Findings = append(report.Findings, Finding{
			Check: "named symbol is not in the index", Detail: name,
		})
	}

	return report, nil
}

func missingPaths(root, answer string) []string {
	var out []string

	for _, path := range dedupe(pathPattern.FindAllString(answer, -1)) {
		if strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
			continue
		}

		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(path))); err != nil {
			out = append(out, path)
		}
	}

	return out
}

func missingSymbols(ctx context.Context, answer string, index Index) ([]string, error) {
	if index == nil {
		return nil, nil
	}

	var out []string

	for _, match := range symbolPattern.FindAllStringSubmatch(answer, -1) {
		name := lastSegment(match[1])
		if len(name) < minSymbolLen {
			continue
		}

		known, err := indexHolds(ctx, index, name)
		if err != nil {
			return nil, err
		}

		if !known {
			out = append(out, match[1])
		}
	}

	return dedupe(out), nil
}

// minSymbolLen keeps one- and two-letter backticked tokens out. They are
// almost always a variable in an example rather than a declaration the
// index would hold.
const minSymbolLen = 3

// indexHolds reports whether the project holds the name at all: declared as
// a symbol, or failing that written somewhere in the tree.
//
// The text half is what keeps the check to its purpose, which is a name a
// run invented. The index holds functions, methods, and types, so a run
// naming a const, a var, a struct field, a config key, or a tool was told
// it had made the name up: `maxReadFiles`, `ErrNoChange`, `AllowedCommands`,
// `IsError`, `hookTimeoutMs`, and `str_replace` were each reported that way,
// and every one of them is written in this project. A name in neither half
// is the one the check is for.
func indexHolds(ctx context.Context, index Index, name string) (bool, error) {
	declared, err := index.DeclaresName(ctx, name)
	if err != nil {
		return false, fmt.Errorf("looking up %s: %w", name, err)
	}

	if declared {
		return true, nil
	}

	results, err := index.Search(ctx, codeintel.SearchQuery{
		Mode: codeintel.SearchLiteral, Text: name, Limit: 1,
	})
	if err != nil {
		if errors.Is(err, codeintel.ErrLiteralTooShort) {
			return false, nil
		}

		return false, fmt.Errorf("looking for %s in the tree: %w", name, err)
	}

	return len(results) > 0, nil
}

// lastSegment is the identifier out of a qualified name, since the index
// stores `Classify` and the answer may write `guard.Classify`.
func lastSegment(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}

	return name
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))

	for _, s := range in {
		if seen[s] {
			continue
		}

		seen[s] = true

		out = append(out, s)
	}

	sort.Strings(out)

	return out
}
