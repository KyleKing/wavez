// Package mention expands `@file` and `@symbol` references in a user's
// prompt into the content they name, so a turn starts with the file or the
// symbol already in context instead of spending a read or a search call to
// find it.
//
// Two rules shape everything here. A mention that does not resolve is
// reported and left in the prompt as literal text, never dropped, because a
// model handed a prompt with the reference silently removed cannot tell that
// it is missing anything. And every expansion is bounded: the target is an
// 8B model with an 8k served window, so a mention of a 3000-line file
// expands to a stated prefix of it plus a note naming what was cut, never to
// the whole file and never to a silent truncation.
package mention

import (
	"context"
	"fmt"
	"strings"

	"github.com/kyleking/wavez/internal/codeintel"
)

// Index is the code index symbol mentions resolve against. Search refreshes
// the index as a side effect, so a mention can never expand from an index
// that has drifted from the tree. A nil Index is allowed and makes every
// symbol mention resolve to an unresolved report naming the missing index.
type Index interface {
	Search(ctx context.Context, q codeintel.SearchQuery) ([]codeintel.SearchResult, codeintel.IndexStats, error)
}

// Kind classifies what a mention resolved to.
type Kind string

const (
	// KindFile is a mention that expanded to file content.
	KindFile Kind = "file"
	// KindSymbol is a mention that expanded to one or more symbol
	// declarations.
	KindSymbol Kind = "symbol"
	// KindUnresolved is a mention that named nothing the expander could
	// find. Its text stays in the prompt and its Detail says why.
	KindUnresolved Kind = "unresolved"
)

// Mention is one reference the expander handled, for a caller that wants to
// show the user what a prompt pulled in.
type Mention struct {
	// Ref is the reference as written, without the leading `@`.
	Ref string
	// Detail is a one-line summary: what was expanded, or why nothing was.
	Detail    string
	Kind      Kind
	Truncated bool
}

// Result is one expansion.
type Result struct {
	// Prompt is the original text with the mention block appended. It equals
	// the input when the prompt held no mentions.
	Prompt   string
	Mentions []Mention
}

// Unresolved returns the mentions that resolved to nothing.
func (r Result) Unresolved() []Mention {
	var out []Mention
	for _, m := range r.Mentions {
		if m.Kind == KindUnresolved {
			out = append(out, m)
		}
	}

	return out
}

// Expander expands mentions in a prompt against a project root and a code
// index.
type Expander struct {
	index       Index
	root        string
	fileLines   int
	totalLines  int
	maxMentions int
}

// Budget defaults, in lines, sized against an 8k served window: one mention
// cannot spend more than roughly a fifth of it and all of them together
// cannot spend more than half, leaving room for the system prompt, the
// thread, and the reply.
const (
	defaultFileLines   = 150
	defaultTotalLines  = 400
	defaultMaxMentions = 20
)

// Option configures an Expander.
type Option func(*Expander)

// WithFileLineBudget caps how many lines one file mention expands to.
// Values below 1 are ignored.
func WithFileLineBudget(lines int) Option {
	return func(e *Expander) {
		if lines > 0 {
			e.fileLines = lines
		}
	}
}

// WithTotalLineBudget caps how many lines of file content all mentions in
// one prompt expand to together. Values below 1 are ignored.
func WithTotalLineBudget(lines int) Option {
	return func(e *Expander) {
		if lines > 0 {
			e.totalLines = lines
		}
	}
}

// WithMaxMentions caps how many references one prompt expands. Values below
// 1 are ignored.
func WithMaxMentions(count int) Option {
	return func(e *Expander) {
		if count > 0 {
			e.maxMentions = count
		}
	}
}

// New builds an Expander for the project rooted at root, resolving symbol
// mentions through index. A nil index disables symbol resolution without
// disabling file mentions.
func New(root string, index Index, opts ...Option) *Expander {
	e := &Expander{
		root:        root,
		index:       index,
		fileLines:   defaultFileLines,
		totalLines:  defaultTotalLines,
		maxMentions: defaultMaxMentions,
	}
	for _, opt := range opts {
		opt(e)
	}

	return e
}

// Expand returns prompt with a mention block appended, plus what each
// reference resolved to. The prompt text itself is never rewritten, so an
// unresolved `@foo` reads the same to the model as the user typed it.
//
// It returns an error only when ctx is already done: a file that cannot be
// read or an index that cannot be queried becomes an unresolved mention
// carrying the reason, because one bad reference must not cost the user
// their turn.
func (e *Expander) Expand(ctx context.Context, prompt string) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("expanding mentions: %w", err)
	}

	refs := scan(prompt)
	if len(refs) == 0 {
		return Result{Prompt: prompt}, nil
	}

	result := Result{Mentions: make([]Mention, 0, len(refs))}
	sections := make([]string, 0, len(refs))
	remaining := e.totalLines

	for i, ref := range refs {
		if i >= e.maxMentions {
			over := e.overflow(refs[i:])
			result.Mentions = append(result.Mentions, over.mentions...)
			sections = append(sections, over.section)

			break
		}

		exp := e.resolve(ctx, ref, remaining)
		remaining -= exp.lines
		result.Mentions = append(result.Mentions, exp.mentions...)
		sections = append(sections, exp.section)
	}

	result.Prompt = prompt + "\n\n--- mentions ---\n" + strings.Join(sections, "\n\n")

	return result, nil
}

func (e *Expander) overflow(refs []string) expansion {
	mentions := make([]Mention, 0, len(refs))
	detail := fmt.Sprintf("not expanded: a prompt expands at most %d mentions", e.maxMentions)
	names := make([]string, 0, len(refs))

	for _, ref := range refs {
		mentions = append(mentions, Mention{Ref: ref, Kind: KindUnresolved, Detail: detail})
		names = append(names, "@"+ref)
	}

	return expansion{
		mentions: mentions,
		section: fmt.Sprintf("%s: %s. Name them in a follow-up turn instead",
			strings.Join(names, ", "), detail),
		handled: true,
	}
}

// resolve tries ref as a path first and as a symbol only when no file
// answers to it, so a name that is both never turns into a guess.
func (e *Expander) resolve(ctx context.Context, ref string, budget int) expansion {
	if exp := e.resolveFile(ref, budget); exp.handled {
		return exp
	}

	return e.resolveSymbol(ctx, ref)
}
