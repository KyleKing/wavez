package models_test

import (
	"testing"

	"github.com/kyleking/gh-repo-dashboard/internal/models"
)

func TestVCSTypeString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		vcs      models.VCSType
		expected string
	}{
		{models.VCSTypeGit, "git"},
		{models.VCSTypeJJ, "jj"},
	}

	for _, tt := range tests {
		if tt.vcs.String() != tt.expected {
			t.Errorf("expected %s, got %s", tt.expected, tt.vcs.String())
		}
	}
}

func TestFilterModeString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		mode     models.FilterMode
		expected string
	}{
		{models.FilterModeAll, "All"},
		{models.FilterModeAhead, "Ahead"},
		{models.FilterModeBehind, "Behind"},
		{models.FilterModeDirty, "Dirty"},
		{models.FilterModeHasPR, "Has PR"},
		{models.FilterModeHasStash, "Has Stash"},
		{models.FilterModeHasNotes, "Has Notes"},
	}

	for _, tt := range tests {
		if tt.mode.String() != tt.expected {
			t.Errorf("expected %s, got %s", tt.expected, tt.mode.String())
		}
	}
}

func TestFilterModeShortKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		mode     models.FilterMode
		expected string
	}{
		{models.FilterModeAll, "a"},
		{models.FilterModeAhead, ">"},
		{models.FilterModeBehind, "<"},
		{models.FilterModeDirty, "d"},
		{models.FilterModeHasPR, "p"},
		{models.FilterModeHasStash, "s"},
		{models.FilterModeHasNotes, "n"},
	}

	for _, tt := range tests {
		if tt.mode.ShortKey() != tt.expected {
			t.Errorf("FilterMode %v: expected %s, got %s", tt.mode, tt.expected, tt.mode.ShortKey())
		}
	}
}

func TestAllFilterModes(t *testing.T) {
	t.Parallel()
	modes := models.AllFilterModes()
	if len(modes) != 7 {
		t.Errorf("expected 7 filter modes, got %d", len(modes))
	}
}

func TestSortModeString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		mode     models.SortMode
		expected string
	}{
		{models.SortModeName, "Name"},
		{models.SortModeModified, "Modified"},
		{models.SortModeStatus, "Status"},
		{models.SortModeBranch, "Branch"},
	}

	for _, tt := range tests {
		if tt.mode.String() != tt.expected {
			t.Errorf("expected %s, got %s", tt.expected, tt.mode.String())
		}
	}
}

func TestSortModeNext(t *testing.T) {
	t.Parallel()
	tests := []struct {
		mode     models.SortMode
		expected models.SortMode
	}{
		{models.SortModeName, models.SortModeModified},
		{models.SortModeModified, models.SortModeStatus},
		{models.SortModeStatus, models.SortModeBranch},
		{models.SortModeBranch, models.SortModeName},
	}

	for _, tt := range tests {
		if tt.mode.Next() != tt.expected {
			t.Errorf("SortMode %v.Next(): expected %v, got %v", tt.mode, tt.expected, tt.mode.Next())
		}
	}
}

func TestRepoStatusString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		status   models.RepoStatus
		expected string
	}{
		{models.RepoStatusClean, "clean"},
		{models.RepoStatusDirty, "dirty"},
		{models.RepoStatusAhead, "ahead"},
		{models.RepoStatusBehind, "behind"},
		{models.RepoStatusDiverged, "diverged"},
	}

	for _, tt := range tests {
		if tt.status.String() != tt.expected {
			t.Errorf("expected %s, got %s", tt.expected, tt.status.String())
		}
	}
}

func TestItemKindString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		kind     models.ItemKind
		expected string
	}{
		{models.ItemKindBranch, "branch"},
		{models.ItemKindStash, "stash"},
		{models.ItemKindWorktree, "worktree"},
	}

	for _, tt := range tests {
		if tt.kind.String() != tt.expected {
			t.Errorf("expected %s, got %s", tt.expected, tt.kind.String())
		}
	}
}
