package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/kyleking/wavez/internal/app"
	"github.com/kyleking/wavez/internal/router"
	"github.com/kyleking/wavez/internal/tool"
)

// The kinds a preamble section falls into, ordered as the rollup prints
// them: what the harness says, what the project says, and what the tool
// surface costs to advertise.
const (
	kindSystem  = "system"
	kindContext = "context"
	kindTool    = "tool"
	kindSchema  = "schema"
	// The description text inside a schema, split out from the structure it
	// hangs on. The split is the point: structure is the grammar a local
	// turn decodes under and cannot be trimmed, and prose is teaching,
	// which can be.
	kindProse = "prose"
)

// section is one accountable part of what every turn pays before the first
// word of the conversation.
type section struct {
	Name  string
	Kind  string
	Bytes int
}

// tokensPerByte is the project's documented char/4 estimate, the same one
// the loop admits a turn against.
const tokensPerByte = 4

// preambleReport accounts for the fixed prefix byte by byte. Every turn pays
// it again, so it is the one cost that scales with turns rather than with
// work, and a trim is only worth making if it can be seen here first.
func preambleReport(ctx context.Context, root string, opt options) error {
	cfg, err := loadConfig(ctx, root, opt.with)
	if err != nil {
		return err
	}

	a, err := app.New(ctx, root, cfg, permissionGate(true), app.WithAsker(stdinAsker{}))
	if err != nil {
		return fmt.Errorf("building project: %w", err)
	}
	//nolint:contextcheck // shutdown must outlive the caller's context, as in headlessRun
	defer func() {
		if cerr := a.Close(); cerr != nil {
			fmt.Fprintf(os.Stderr, "wavez: shutdown: %v\n", cerr)
		}
	}()

	sections, err := sectionsOf(root, cfg.Context, a.Tools)
	if err != nil {
		return err
	}

	fastOnly := withoutTools(sections, app.FastTierOmits)

	if err := writePreamble(os.Stdout, sections, fastOnly); err != nil {
		return err
	}

	// The ceiling holds the fast tier's prefix, which is the only one where
	// it decides anything: the same bytes are 33% of what a fast turn can
	// use and under 2% of a hosted one.
	return withinBudget(fastOnly, opt.preambleMax)
}

// errPreambleOverBudget reports a fixed prefix past the ceiling CI holds it
// to.
var errPreambleOverBudget = errors.New("preamble is over budget")

// withinBudget fails when the fixed prefix costs more than ceiling tokens. The
// prefix is 41% of what a fast turn can use and only shrinks when somebody
// remembers to look, so a ceiling that fails the build makes every new
// tool's cost a decision rather than a discovery.
func withinBudget(sections []section, ceiling int) error {
	if ceiling <= 0 {
		return nil
	}

	total := 0
	for _, s := range sections {
		total += s.Bytes
	}

	tokens := total / tokensPerByte
	if tokens <= ceiling {
		return nil
	}

	return fmt.Errorf("%w: %d tokens against a ceiling of %d. Trim a section above, or raise "+
		"the ceiling deliberately and say what bought the room", errPreambleOverBudget, tokens, ceiling)
}

// sectionsOf sizes each part on its own. BuildPrefix joins the context
// entries and reports nothing about which one cost what, and the per-entry
// number is the whole point of the audit, so each entry is built alone.
func sectionsOf(root string, entries []string, registry *tool.Registry) ([]section, error) {
	out := []section{{Name: "system rules", Kind: kindSystem, Bytes: len(app.BaseSystem)}}

	for _, entry := range entries {
		part, err := app.BuildPrefix(root, []string{entry})
		if err != nil {
			return nil, fmt.Errorf("building the project context: %w", err)
		}

		out = append(out, section{Name: entry, Kind: kindContext, Bytes: len(part)})
	}

	for _, s := range registry.Specs() {
		cost, err := splitSchema(s.Schema)
		if err != nil {
			return nil, fmt.Errorf("reading %s's schema: %w", s.Name, err)
		}

		out = append(out,
			section{Name: s.Name + " (text)", Kind: kindTool, Bytes: len(s.Name) + len(s.Description)},
			section{Name: s.Name + " (schema prose)", Kind: kindProse, Bytes: cost.Prose},
			section{Name: s.Name + " (schema)", Kind: kindSchema, Bytes: cost.Structure})
	}

	return out, nil
}

// splitSchema separates a JSON Schema's description prose from the
// structure it describes, by re-marshaling the document with every
// description removed.
//
// The two are different kinds of cost. Structure is what llama.cpp compiles
// into the grammar a fast turn decodes under, so it buys correctness on
// every call. Prose is teaching, and teaching that only says what a failure
// will say is paid on every turn of every thread to prevent a failure that
// pays for itself once.
// The two halves are derived rather than counted so they always add up to
// the schema the model is actually sent: prose is what removing every
// description saves, which includes the key and quoting each one costs.
func splitSchema(raw json.RawMessage) (schemaCost, error) {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return schemaCost{}, fmt.Errorf("parsing the schema: %w", err)
	}

	body, err := json.Marshal(stripDescriptions(doc))
	if err != nil {
		return schemaCost{}, fmt.Errorf("re-encoding the schema: %w", err)
	}

	return schemaCost{Prose: len(raw) - len(body), Structure: len(body)}, nil
}

// schemaCost is one schema's two halves in bytes.
type schemaCost struct {
	Prose     int
	Structure int
}

func stripDescriptions(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))

		for k, val := range t {
			if k == "description" {
				if _, ok := val.(string); ok {
					continue
				}
			}

			out[k] = stripDescriptions(val)
		}

		return out
	case []any:
		out := make([]any, 0, len(t))
		for _, e := range t {
			out = append(out, stripDescriptions(e))
		}

		return out
	default:
		return v
	}
}

// withoutTools is the prefix as one tier sees it, with every section
// belonging to a tool that tier is not shown removed.
func withoutTools(sections []section, omit []string) []section {
	drop := make(map[string]bool, len(omit))
	for _, name := range omit {
		drop[name] = true
	}

	out := make([]section, 0, len(sections))

	for _, s := range sections {
		if name, _, found := strings.Cut(s.Name, " ("); found && drop[name] {
			continue
		}

		out = append(out, s)
	}

	return out
}

func writePreamble(w io.Writer, sections, fastOnly []section) error {
	total := bytesOf(sections)
	byKind := map[string]int{}

	for _, s := range sections {
		byKind[s.Kind] += s.Bytes
	}

	sort.Slice(sections, func(i, j int) bool { return sections[i].Bytes > sections[j].Bytes })

	p := &printer{w: w}
	p.printf("%-34s %-8s %8s %8s %7s\n", "section", "kind", "bytes", "~tokens", "share")

	for _, s := range sections {
		p.printf("%-34s %-8s %8d %8d %6.1f%%\n",
			s.Name, s.Kind, s.Bytes, s.Bytes/tokensPerByte, share(s.Bytes, total))
	}

	p.printf("\n%-34s %-8s %8d %8d\n", "total", "", total, total/tokensPerByte)

	for _, kind := range []string{kindSystem, kindContext, kindTool, kindProse, kindSchema} {
		p.printf("  %-32s %8d %8d %6.1f%%\n",
			kind, byKind[kind], byKind[kind]/tokensPerByte, share(byKind[kind], total))
	}

	// The window is the constraint the size actually matters against, and it
	// is not one number: the same prefix is a third of what a fast turn can
	// use and noise on a hosted one, so each tier is reported against its
	// own window and against the surface it is actually shown.
	usable := router.FastContextBudget - router.ReplyReserve
	fastTokens := bytesOf(fastOnly) / tokensPerByte
	p.printf("\nfast   %5d tokens of %6d usable (%.0f%%)\n",
		fastTokens, usable, share(fastTokens, usable))
	p.printf("hosted %5d tokens of %6d usable (%.1f%%)\n",
		total/tokensPerByte, router.HostedContextBudget,
		share(total/tokensPerByte, router.HostedContextBudget))

	return p.err
}

func bytesOf(sections []section) int {
	total := 0
	for _, s := range sections {
		total += s.Bytes
	}

	return total
}

// printer keeps the first write error and stops caring about the rest, so a
// closed pipe is reported once rather than checked on every row.
type printer struct {
	w   io.Writer
	err error
}

func (p *printer) printf(format string, args ...any) {
	if p.err != nil {
		return
	}

	if _, err := fmt.Fprintf(p.w, format, args...); err != nil {
		p.err = fmt.Errorf("writing the preamble report: %w", err)
	}
}

func share(n, total int) float64 {
	if total == 0 {
		return 0
	}

	return float64(n) / float64(total) * 100 //nolint:mnd // a percentage
}
