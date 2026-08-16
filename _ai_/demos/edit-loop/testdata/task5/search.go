// Package search is a trimmed, dependency-free copy of gh-repo-dashboard's
// internal/filters/search.go SearchRepos, adapted for the edit-loop spike.
package search

import (
	"path/filepath"
	"strings"
)

// SearchRepos filters paths to those whose base name matches searchText,
// preferring exact base-name matches over substring matches.
func SearchRepos(paths []string, searchText string) []string {
	if searchText == "" {
		return paths
	}

	searchLower := strings.ToLower(searchText)

	var exact []string
	var substring []string

	for _, path := range paths {
		if strings.ToLower(filepath.Base(path)) == searchLower {
			exact = append(exact, path)
		} else if strings.Contains(strings.ToLower(filepath.Base(path)), searchLower) {
			substring = append(substring, path)
		}
	}

	if len(exact) > 0 {
		return exact
	}

	return substring
}
