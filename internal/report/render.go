package report

import (
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"strings"
)

var (
	//go:embed page.tmpl
	pageSource string
	//go:embed bench.tmpl
	benchSource string
	//go:embed style.css
	styleSource string
)

// templateFuncs hands the shared stylesheet to both pages.
var templateFuncs = template.FuncMap{
	//nolint:gosec // the stylesheet is this package's own file, not input
	"style": func() template.CSS { return template.CSS(styleSource) },
}

var (
	pageTemplate  = template.Must(template.New("page").Funcs(templateFuncs).Parse(pageSource))
	benchTemplate = template.Must(template.New("bench").Funcs(templateFuncs).Parse(benchSource))
)

// HTML writes the page as one self-contained document: its styles and
// script are inline, and it asks the network for nothing.
func (p *Page) HTML(w io.Writer) error {
	if err := pageTemplate.Execute(w, p); err != nil {
		return fmt.Errorf("rendering the session page: %w", err)
	}

	return nil
}

// Text writes what needs the person, where the session went, and the
// waste, as plain text for a terminal.
func (p *Page) Text(w io.Writer) error {
	var b strings.Builder

	fmt.Fprintf(&b, "%s  session %s  %s\n", p.Project, p.Short, p.Date)

	for _, f := range p.Stats {
		fmt.Fprintf(&b, "  %-9s %s%s\n", strings.ToLower(f.Label), f.Value, note(f.Note))
	}

	if len(p.Needs) > 0 {
		b.WriteString("\nNEEDS YOU\n")
		textItems(&b, p.Needs)
	}

	b.WriteString("\nWHERE IT WENT\n")
	const row = "  %-18s %6s %6s %6s %14s %14s\n"

	fmt.Fprintf(&b, row, "kind", "steps", "share", "turns", "output tokens", "input tokens")

	for i := range p.Kinds {
		k := &p.Kinds[i]
		fmt.Fprintf(&b, row, strings.ToLower(k.Name), k.Steps, k.Share, k.Turns, k.Output, k.Input)
	}

	for _, f := range p.Facts {
		fmt.Fprintf(&b, "  %s: %s%s\n", strings.ToLower(f.Label), f.Value, note(f.Note))
	}

	b.WriteString("\nWASTE\n")

	if len(p.Waste) == 0 {
		b.WriteString("  none of the patterns this report looks for turned up\n")
	}

	textItems(&b, p.Waste)

	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("writing the session report: %w", err)
	}

	return nil
}

func textItems(b *strings.Builder, items []Item) {
	for _, item := range items {
		fmt.Fprintf(b, "  - %s\n    %s\n", item.Title, item.Detail)

		if item.Copy != "" {
			fmt.Fprintf(b, "    say: %s\n", item.Copy)
		}
	}
}

func note(text string) string {
	if text == "" {
		return ""
	}

	return " (" + text + ")"
}
