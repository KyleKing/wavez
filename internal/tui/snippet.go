package tui

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/kyleking/wavez/internal/snippet"
)

const (
	snippetSaveUserPrefix = "snippet save --user "
	snippetSavePrefix     = "snippet save "
	verbSnippetList       = "snippet list"
)

var paletteSnippetVerbs = []string{"snippet save", "snippet save --user", verbSnippetList}

// completeSnippet expands the word before the cursor on Tab: a unique
// match splices in the snippet, several complete to their longest common
// prefix and list the candidates in the status line, none leaves the
// buffer untouched.
func (m Model) completeSnippet() Model {
	start, prefix := m.thread.input.wordBeforeCursor()
	if prefix == "" {
		return m
	}

	to := m.thread.input.cur.col

	names := matchingSnippetNames(m.snippets, prefix)

	switch len(names) {
	case 0:
		return m
	case 1:
		m.thread.input.snapshot()
		m.thread.input.replaceSpan(start, to, m.snippets[names[0]])
		m.status = ""
	default:
		if lcp := longestCommonPrefix(names); len(lcp) > len(prefix) {
			m.thread.input.snapshot()
			m.thread.input.replaceSpan(start, to, lcp)
		}

		m.status = "snippets: " + strings.Join(names, ", ")
	}

	return m
}

func matchingSnippetNames(snippets map[string]string, prefix string) []string {
	var names []string

	for name := range snippets {
		if wordsMatch(name, prefix) {
			names = append(names, name)
		}
	}

	sort.Strings(names)

	return names
}

func longestCommonPrefix(names []string) string {
	if len(names) == 0 {
		return ""
	}

	prefix := names[0]
	for _, n := range names[1:] {
		for !strings.HasPrefix(n, prefix) {
			prefix = prefix[:len(prefix)-1]
		}
	}

	return prefix
}

// parseSnippetSave parses "snippet save [--user] <name>", returning the
// name, whether --user was given, and whether the input matched.
func parseSnippetSave(input string) (string, bool, bool) {
	trimmed := strings.TrimSpace(input)

	switch {
	case strings.HasPrefix(trimmed, snippetSaveUserPrefix):
		name := strings.TrimSpace(strings.TrimPrefix(trimmed, snippetSaveUserPrefix))

		return name, true, name != ""
	case strings.HasPrefix(trimmed, snippetSavePrefix):
		name := strings.TrimSpace(strings.TrimPrefix(trimmed, snippetSavePrefix))

		return name, false, name != ""
	default:
		return "", false, false
	}
}

func (m Model) saveSnippet(name string, user bool) (Model, tea.Cmd) {
	m.palette.open = false
	m.palette.input.Reset()

	path := snippet.RepoPath(m.dir)

	if user {
		p, err := snippet.UserPath()
		if err != nil {
			m.status = "saving snippet: " + err.Error()

			return m, nil
		}

		path = p
	}

	existing, err := snippet.Load(path)
	if err != nil {
		m.status = "saving snippet: " + err.Error()

		return m, nil
	}

	if existing == nil {
		existing = map[string]string{}
	}

	existing[name] = m.thread.input.Value()

	if err := snippet.Save(path, existing); err != nil {
		m.status = "saving snippet: " + err.Error()

		return m, nil
	}

	merged, err := snippet.LoadAll(m.dir)
	if err != nil {
		m.status = "saving snippet: " + err.Error()

		return m, nil
	}

	m.snippets = merged
	m.status = fmt.Sprintf("saved snippet %q", name)

	return m, nil
}

func (m Model) listSnippets() Model {
	if len(m.snippets) == 0 {
		m.status = "no saved snippets"

		return m
	}

	names := make([]string, 0, len(m.snippets))
	for name := range m.snippets {
		names = append(names, name)
	}

	sort.Strings(names)

	m.status = "snippets: " + strings.Join(names, ", ")

	return m
}
