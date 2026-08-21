package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/kyleking/wavez/internal/app"
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

	return writePreamble(os.Stdout, sections)
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
		out = append(out,
			section{Name: s.Name + " (text)", Kind: kindTool, Bytes: len(s.Name) + len(s.Description)},
			section{Name: s.Name + " (schema)", Kind: kindSchema, Bytes: len(s.Schema)})
	}

	return out, nil
}

func writePreamble(w io.Writer, sections []section) error {
	total := 0
	byKind := map[string]int{}

	for _, s := range sections {
		total += s.Bytes
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

	for _, kind := range []string{kindSystem, kindContext, kindTool, kindSchema} {
		p.printf("  %-32s %8d %8d %6.1f%%\n",
			kind, byKind[kind], byKind[kind]/tokensPerByte, share(byKind[kind], total))
	}

	return p.err
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
