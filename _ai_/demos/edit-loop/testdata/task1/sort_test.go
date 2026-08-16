package sortutil

import "testing"

func TestSortPathsDirtyFirst(t *testing.T) {
	t.Parallel()
	paths := []string{"/clean", "/dirty-few", "/dirty-many"}
	summaries := map[string]RepoSummary{
		"/clean":      {Path: "/clean", Dirty: false},
		"/dirty-few":  {Path: "/dirty-few", Dirty: true, Uncommitted: 2},
		"/dirty-many": {Path: "/dirty-many", Dirty: true, Uncommitted: 9},
	}

	got := SortPaths(paths, summaries)
	want := []string{"/dirty-many", "/dirty-few", "/clean"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected order %v, got %v", want, got)
		}
	}
}

func TestCompareByStatusTiesBreakByName(t *testing.T) {
	t.Parallel()
	a := &RepoSummary{Path: "/b-repo", Dirty: true, Uncommitted: 3}
	b := &RepoSummary{Path: "/a-repo", Dirty: true, Uncommitted: 3}

	if compareByStatus(a, b) {
		t.Errorf("expected /a-repo to sort before /b-repo on a tie")
	}
}
