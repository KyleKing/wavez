package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/kyleking/wavez/internal/codeintel"
	"github.com/kyleking/wavez/internal/tool"
)

var contextSchema = buildSchema(map[string]schemaProperty{
	"files": {
		Type: schemaTypeString,
		Description: "Comma-separated files to build context around, relative to the project " +
			"root as search prints them. Append a line range to narrow one: " +
			"\"internal/tools/search.go:70-96, internal/tool/tool.go\".",
	},
	"token_budget": {
		Type: schemaTypeInteger,
		Description: "Approximate token cap on the bundle. Defaults to 1200 if omitted or " +
			"zero; the reply says so when the budget cut it short.",
	},
}, "files")

// ContextIndex is the code index Context queries. Context refreshes as a
// side effect of querying, so a caller can never read a bundle built from
// an index that has drifted from the tree.
type ContextIndex interface {
	Refresh(ctx context.Context) (codeintel.IndexStats, error)
	Context(ctx context.Context, req codeintel.ContextRequest) (codeintel.ContextBundle, error)
}

// StoreIndex satisfies ContextIndex by pairing an Indexer's freshness check
// with a Store's bundle query, neither of which carries both halves.
type StoreIndex struct {
	*codeintel.Indexer
	*codeintel.Store
}

// Context is the first-turn bundle tool over the code index: the symbols
// covering the named files, their one-hop neighbors in the symbol graph,
// and the tests whose coverage reaches them, formatted for a small model's
// context window rather than dumped as JSON.
type Context struct {
	index ContextIndex
}

// NewContext builds a Context tool over index.
func NewContext(index ContextIndex) *Context {
	return &Context{index: index}
}

// Name implements tool.Tool.
func (*Context) Name() string { return "context" }

// Description implements tool.Tool.
func (*Context) Description() string {
	return "Get a ranked bundle for files you are about to work on: the symbols covering them, " +
		"their callers and callees one hop out, and the tests that cover them. Call this once " +
		"before reading files, then use read for the bodies you still need."
}

// Schema implements tool.Tool.
func (*Context) Schema() json.RawMessage { return contextSchema }

type contextInput struct {
	Files       string `json:"files"`
	TokenBudget int    `json:"token_budget"`
}

const defaultTokenBudget = 1200

// Run implements tool.Tool.
func (c *Context) Run(ctx context.Context, input json.RawMessage) (tool.Result, error) {
	if err := ctx.Err(); err != nil {
		return tool.Result{}, fmt.Errorf("context: %w", err)
	}

	var in contextInput
	if err := decodeInput(input, &in); err != nil {
		return tool.Fail(tool.CauseMalformed, "invalid input: %v", err), nil
	}

	touched, err := parseTouched(in.Files)
	if err != nil {
		return tool.Fail(tool.CauseBadInput, "%v", err), nil
	}

	budget := in.TokenBudget
	if budget <= 0 {
		budget = defaultTokenBudget
	}

	stats, err := c.index.Refresh(ctx)
	if err != nil {
		return tool.Fail(tool.CauseUpstream, "%v", err), nil
	}

	bundle, err := c.index.Context(ctx, codeintel.ContextRequest{Touched: touched, TokenBudget: budget})
	if err != nil {
		return tool.Fail(tool.CauseUpstream, "%v", err), nil
	}

	return tool.Result{Content: formatBundle(bundle, stats, touched, budget)}, nil
}

// wholeFileEnd stands in for a file's last line when the caller named no
// range, high enough that no source file reaches it.
const wholeFileEnd = 1 << 30

// ErrContextFiles reports a files argument Context could not parse.
var ErrContextFiles = errors.New("invalid files")

func parseTouched(files string) ([]codeintel.TouchedRange, error) {
	var touched []codeintel.TouchedRange

	for _, field := range strings.Split(files, ",") {
		entry := strings.TrimSpace(field)
		if entry == "" {
			continue
		}

		r, err := parseTouchedEntry(entry)
		if err != nil {
			return nil, err
		}
		touched = append(touched, r)
	}

	if len(touched) == 0 {
		return nil, fmt.Errorf("%w: name at least one file, optionally as path:start-end", ErrContextFiles)
	}

	return touched, nil
}

func parseTouchedEntry(entry string) (codeintel.TouchedRange, error) {
	path, spec, found := strings.Cut(entry, ":")
	if !found {
		return codeintel.TouchedRange{File: entry, Start: 1, End: wholeFileEnd}, nil
	}

	startText, endText, hasEnd := strings.Cut(spec, "-")

	start, err := strconv.Atoi(startText)
	if err != nil || start < 1 {
		return codeintel.TouchedRange{},
			fmt.Errorf("%w: %q wants path:start-end with 1-indexed lines", ErrContextFiles, entry)
	}

	end := start
	if hasEnd {
		end, err = strconv.Atoi(endText)
		if err != nil || end < start {
			return codeintel.TouchedRange{}, fmt.Errorf("%w: %q ends before it starts", ErrContextFiles, entry)
		}
	}

	return codeintel.TouchedRange{File: path, Start: start, End: end}, nil
}

func formatTouched(touched []codeintel.TouchedRange) string {
	parts := make([]string, 0, len(touched))
	for _, r := range touched {
		if r.End == wholeFileEnd {
			parts = append(parts, r.File)

			continue
		}
		parts = append(parts, fmt.Sprintf("%s:%d-%d", r.File, r.Start, r.End))
	}

	return strings.Join(parts, ", ")
}

// formatBundle distinguishes files the index covers no symbol of from an
// index that covers nothing at all, the same split search reports: told
// that both are "no results", a model narrows a query that could not have
// matched anything.
func formatBundle(
	bundle codeintel.ContextBundle,
	stats codeintel.IndexStats,
	touched []codeintel.TouchedRange,
	budget int,
) string {
	where := formatTouched(touched)

	if len(bundle.Symbols) == 0 && len(bundle.Neighbors) == 0 && len(bundle.Tests) == 0 {
		switch {
		case stats.Building != "":
			return "no context: " + stats.Building
		case stats.FilesScanned == 0:
			return "no context: the code index covers no files in this project, " +
				"so context cannot answer here. Use shell with rg, or read, instead"
		case bundle.Truncated:
			return fmt.Sprintf("no context: a budget of %d tokens is too small for %s", budget, where)
		default:
			return fmt.Sprintf("no indexed symbols cover %s across %d indexed files", where, stats.FilesScanned)
		}
	}

	var b strings.Builder

	fmt.Fprintf(&b, "context %s: %d symbols, %d edges, %d tests, ~%d tokens\n",
		where, len(bundle.Symbols), len(bundle.Neighbors), len(bundle.Tests), bundle.TokensUsed)

	writeBundleSymbols(&b, bundle.Symbols)
	writeBundleEdges(&b, bundle.Neighbors)
	writeBundleTests(&b, bundle.Tests)

	if bundle.Truncated {
		fmt.Fprintf(&b, "truncated at the %d token budget; narrow the line range or raise token_budget\n", budget)
	}

	return strings.TrimSuffix(b.String(), "\n")
}

func writeBundleSymbols(b *strings.Builder, symbols []codeintel.Symbol) {
	if len(symbols) == 0 {
		return
	}

	b.WriteString("symbols\n")
	for i := range symbols {
		sym := &symbols[i]
		fmt.Fprintf(b, "  %s %s %s:%d-%d", sym.Kind, sym.Name, sym.FilePath, sym.StartLine, sym.EndLine)
		if sig := flattenSignature(sym.Signature); sig != "" {
			fmt.Fprintf(b, " %s", sig)
		}
		b.WriteString("\n")
	}
}

// maxSignatureLen keeps one symbol to one line: a tree-sitter signature
// carries the whole declaration, so a struct's fields arrive with it.
const maxSignatureLen = 120

func flattenSignature(sig string) string {
	flat := []rune(strings.Join(strings.Fields(sig), " "))
	if len(flat) > maxSignatureLen {
		return string(flat[:maxSignatureLen]) + "…"
	}

	return string(flat)
}

func writeBundleEdges(b *strings.Builder, edges []codeintel.Edge) {
	if len(edges) == 0 {
		return
	}

	b.WriteString("edges\n")
	for _, e := range edges {
		fmt.Fprintf(b, "  %s %s -> %s (%.2f)\n", e.Kind, e.Src, e.Dst, e.Confidence)
	}
}

func writeBundleTests(b *strings.Builder, tests []codeintel.CoverageTest) {
	if len(tests) == 0 {
		return
	}

	b.WriteString("tests\n")
	for _, t := range tests {
		fmt.Fprintf(b, "  %s\n", t.TestID)
	}
}
