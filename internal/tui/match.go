package tui

import "strings"

// wordsMatch reports whether every word in query appears somewhere in
// label, case-insensitively and in any order, so word order in the query
// does not matter. It is the fuzzy matcher the command palette and the
// transcript's fuzzy row search share.
func wordsMatch(label, query string) bool {
	label = strings.ToLower(label)

	for _, w := range strings.Fields(strings.ToLower(query)) {
		if !strings.Contains(label, w) {
			return false
		}
	}

	return true
}
