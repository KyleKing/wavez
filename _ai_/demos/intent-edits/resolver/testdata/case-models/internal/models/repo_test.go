package models_test

import (
	"testing"
	"time"

	"github.com/kyleking/gh-repo-dashboard/internal/models"
)

func TestRepoSummaryName(t *testing.T) {
	t.Parallel()
	s := models.RepoSummary{Path: "/home/user/projects/my-repo"}
	if s.Name() != "my-repo" {
		t.Errorf("expected 'my-repo', got '%s'", s.Name())
	}
}

func TestRepoSummaryUncommittedCount(t *testing.T) {
	t.Parallel()
	s := models.RepoSummary{
		Staged:     2,
		Unstaged:   3,
		Untracked:  1,
		Conflicted: 0,
	}
	if s.UncommittedCount() != 6 {
		t.Errorf("expected 6, got %d", s.UncommittedCount())
	}
}

func TestRepoSummaryIsDirty(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		summary  models.RepoSummary
		expected bool
	}{
		{
			name:     "clean repo",
			summary:  models.RepoSummary{},
			expected: false,
		},
		{
			name:     "has staged",
			summary:  models.RepoSummary{Staged: 1},
			expected: true,
		},
		{
			name:     "has unstaged",
			summary:  models.RepoSummary{Unstaged: 1},
			expected: true,
		},
		{
			name:     "has untracked",
			summary:  models.RepoSummary{Untracked: 1},
			expected: true,
		},
		{
			name:     "has ahead",
			summary:  models.RepoSummary{Ahead: 1},
			expected: true,
		},
		{
			name:     "only behind is not dirty",
			summary:  models.RepoSummary{Behind: 1},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.summary.IsDirty() != tt.expected {
				t.Errorf("expected IsDirty() = %v, got %v", tt.expected, tt.summary.IsDirty())
			}
		})
	}
}

func TestRepoSummaryStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		summary  models.RepoSummary
		expected models.RepoStatus
	}{
		{
			name:     "clean",
			summary:  models.RepoSummary{},
			expected: models.RepoStatusClean,
		},
		{
			name:     "dirty",
			summary:  models.RepoSummary{Unstaged: 1},
			expected: models.RepoStatusDirty,
		},
		{
			name:     "ahead",
			summary:  models.RepoSummary{Ahead: 1},
			expected: models.RepoStatusAhead,
		},
		{
			name:     "behind",
			summary:  models.RepoSummary{Behind: 1},
			expected: models.RepoStatusBehind,
		},
		{
			name:     "diverged",
			summary:  models.RepoSummary{Ahead: 1, Behind: 1},
			expected: models.RepoStatusDiverged,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.summary.Status() != tt.expected {
				t.Errorf("expected Status() = %v, got %v", tt.expected, tt.summary.Status())
			}
		})
	}
}

func TestRepoSummaryStatusSummary(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		summary  models.RepoSummary
		expected string
	}{
		{
			name:     "clean",
			summary:  models.RepoSummary{},
			expected: "✓",
		},
		{
			name:     "staged only",
			summary:  models.RepoSummary{Staged: 2},
			expected: "+2",
		},
		{
			name:     "unstaged only",
			summary:  models.RepoSummary{Unstaged: 3},
			expected: "~3",
		},
		{
			name:     "untracked only",
			summary:  models.RepoSummary{Untracked: 1},
			expected: "?1",
		},
		{
			name:     "ahead only",
			summary:  models.RepoSummary{Ahead: 5},
			expected: "↑5",
		},
		{
			name:     "behind only",
			summary:  models.RepoSummary{Behind: 3},
			expected: "↓3",
		},
		{
			name:     "mixed",
			summary:  models.RepoSummary{Staged: 1, Unstaged: 2, Ahead: 3},
			expected: "+1 ~2 ↑3",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if tt.summary.StatusSummary() != tt.expected {
				t.Errorf("expected StatusSummary() = '%s', got '%s'", tt.expected, tt.summary.StatusSummary())
			}
		})
	}
}

func TestRepoSummaryRelativeModified(t *testing.T) {
	t.Parallel()
	s := models.RepoSummary{}
	if s.RelativeModified() != models.EmDash {
		t.Errorf("expected '—' for zero time, got '%s'", s.RelativeModified())
	}

	s.LastModified = time.Now()
	if s.RelativeModified() == models.EmDash {
		t.Error("expected non-empty relative time")
	}
}
