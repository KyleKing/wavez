package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

// paletteEntry is one selectable row: a thread jump, a directory scope
// jump, a pending prompt, or a static verb.
type paletteEntry struct {
	label  string
	kind   string
	target string
}

// paletteState is the `:` command palette's cursor, filter text, and open
// flag.
type paletteState struct {
	input  textinput.Model
	cursor int
	open   bool
}

func newPaletteState(th theme) paletteState {
	return paletteState{input: th.newInput("jump to a thread, directory, prompt, or verb")}
}

var paletteVerbs = []string{"diagnostics", labelInbox, labelQuit, labelUndo}

// paletteEntries builds the fuzzy-filterable list: threads, directories,
// pending prompts, and verbs, matched by substring against every word in
// the query so word order does not matter.
func (m Model) paletteEntries() []paletteEntry {
	var entries []paletteEntry

	seenDirs := map[string]bool{}

	for i := range m.threads {
		t := &m.threads[i]
		entries = append(entries, paletteEntry{label: t.Dir + "/" + t.Name, kind: "thread", target: t.ID})

		if !seenDirs[t.Dir] {
			seenDirs[t.Dir] = true
			entries = append(entries, paletteEntry{label: t.Dir, kind: "directory", target: t.Dir})
		}
	}

	for i := range m.pending {
		p := &m.pending[i]
		entries = append(entries, paletteEntry{label: p.Thread + " · " + p.Action, kind: "prompt", target: p.ThreadID})
	}

	for _, v := range paletteVerbs {
		entries = append(entries, paletteEntry{label: v, kind: "verb", target: v})
	}

	return filterEntries(entries, m.palette.input.Value())
}

func filterEntries(entries []paletteEntry, query string) []paletteEntry {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return entries
	}

	words := strings.Fields(query)

	var out []paletteEntry

	for _, e := range entries {
		label := strings.ToLower(e.label)

		match := true

		for _, w := range words {
			if !strings.Contains(label, w) {
				match = false

				break
			}
		}

		if match {
			out = append(out, e)
		}
	}

	return out
}

func (m Model) updatePaletteKey(msg tea.KeyPressMsg, s string) (Model, tea.Cmd) {
	entries := m.paletteEntries()

	switch s {
	case keyJ, keyDown:
		m.palette.cursor = min(m.palette.cursor+1, max(len(entries)-1, 0))

		return m, nil
	case "k", "up":
		m.palette.cursor = max(m.palette.cursor-1, 0)

		return m, nil
	case keyEnter:
		if len(entries) == 0 {
			return m, nil
		}

		return m.runPaletteEntry(entries[min(m.palette.cursor, len(entries)-1)])
	}

	var cmd tea.Cmd
	m.palette.input, cmd = m.palette.input.Update(msg)
	m.palette.cursor = 0

	return m, cmd
}

func (m Model) runPaletteEntry(e paletteEntry) (Model, tea.Cmd) {
	m.palette.open = false
	m.palette.input.Reset()

	switch e.kind {
	case "thread", "prompt":
		return m.openThread(e.target)
	case "directory":
		m.dir = e.target

		return m, nil
	case "verb":
		return m.runPaletteVerb(e.target)
	}

	return m, nil
}

func (m Model) runPaletteVerb(verb string) (Model, tea.Cmd) {
	switch verb {
	case labelInbox:
		m.push(screenInbox)
	case "diagnostics":
		m.push(screenDiagnostics)
	case labelQuit:
		m.quitting = true

		return m, tea.Quit
	case labelUndo:
		return m.requestRestore()
	}

	return m, nil
}

func (m Model) renderPalette() string {
	entries := m.paletteEntries()

	var body []string
	body = append(body, ": "+m.palette.input.View(), "")

	for i, e := range entries {
		line := fmt.Sprintf("%-10s %s", e.kind, e.label)
		if i == min(m.palette.cursor, max(len(entries)-1, 0)) {
			body = append(body, m.th.accent.Render("> "+line))
		} else {
			body = append(body, m.th.fgDefault.Render("  "+line))
		}
	}

	if len(entries) == 0 {
		body = append(body, m.th.fgMuted.Render("no matches"))
	}

	footer := footerHints([]hint{{keyEnter, "go"}, {keyEsc, "close"}}, m.width-boxPad)

	return frame(m.width, "palette", body, footer, m.th)
}
