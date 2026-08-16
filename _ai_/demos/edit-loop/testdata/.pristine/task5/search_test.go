package search

import "testing"

func TestSearchReposExactBeforeSubstring(t *testing.T) {
	t.Parallel()
	paths := []string{"/api", "/api-client", "/api-service"}
	got := SearchRepos(paths, "api")
	if len(got) != 1 || got[0] != "/api" {
		t.Fatalf("expected exact match [/api], got %v", got)
	}
}

func TestSearchReposSubstringFallback(t *testing.T) {
	t.Parallel()
	paths := []string{"/api-client", "/api-service", "/web-app"}
	got := SearchRepos(paths, "api")
	if len(got) != 2 {
		t.Fatalf("expected 2 substring matches, got %v", got)
	}
}
