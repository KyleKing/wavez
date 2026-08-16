package models_test

import (
	"testing"
	"time"

	"github.com/kyleking/gh-repo-dashboard/internal/models"
)

func TestPRInfoStatusDisplay(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		pr       models.PRInfo
		expected string
	}{
		{
			name:     "draft pr",
			pr:       models.PRInfo{IsDraft: true, State: "OPEN"},
			expected: "DRAFT",
		},
		{
			name:     "open pr",
			pr:       models.PRInfo{State: "OPEN"},
			expected: "OPEN",
		},
		{
			name:     "merged pr",
			pr:       models.PRInfo{State: "MERGED"},
			expected: "MERGED",
		},
		{
			name:     "closed pr",
			pr:       models.PRInfo{State: "CLOSED"},
			expected: "CLOSED",
		},
		{
			name:     "unknown state passed through",
			pr:       models.PRInfo{State: "UNKNOWN"},
			expected: "UNKNOWN",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := tt.pr.StatusDisplay()
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestPRInfoReviewStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		pr       models.PRInfo
		expected string
	}{
		{
			name:     "approved via decision",
			pr:       models.PRInfo{ReviewDecision: "APPROVED"},
			expected: "approved",
		},
		{
			name:     "changes requested",
			pr:       models.PRInfo{ReviewDecision: "CHANGES_REQUESTED"},
			expected: "changes requested",
		},
		{
			name:     "review required",
			pr:       models.PRInfo{ReviewDecision: "REVIEW_REQUIRED"},
			expected: "review required",
		},
		{
			name:     "approved via approvers list",
			pr:       models.PRInfo{ApprovedBy: []string{"user1"}},
			expected: "approved",
		},
		{
			name:     "no review info",
			pr:       models.PRInfo{},
			expected: models.EmDash,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := tt.pr.ReviewStatus()
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestChecksStatusSummary(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		checks   models.ChecksStatus
		expected string
	}{
		{
			name:     "no checks",
			checks:   models.ChecksStatus{Total: 0},
			expected: models.EmDash,
		},
		{
			name:     "all passing",
			checks:   models.ChecksStatus{Total: 3, Passing: 3},
			expected: "passing",
		},
		{
			name:     "has failures",
			checks:   models.ChecksStatus{Total: 3, Passing: 2, Failing: 1},
			expected: "failing",
		},
		{
			name:     "has pending",
			checks:   models.ChecksStatus{Total: 3, Passing: 2, Pending: 1},
			expected: "pending",
		},
		{
			name:     "mixed (skipped)",
			checks:   models.ChecksStatus{Total: 3, Passing: 2, Skipped: 1},
			expected: "mixed",
		},
		{
			name:     "failing takes priority over pending",
			checks:   models.ChecksStatus{Total: 3, Failing: 1, Pending: 2},
			expected: "failing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := tt.checks.Summary()
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestWorkflowRunStatusDisplay(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		run      models.WorkflowRun
		expected string
	}{
		{
			name:     "completed shows conclusion",
			run:      models.WorkflowRun{Status: "completed", Conclusion: "success"},
			expected: "success",
		},
		{
			name:     "in progress shows status",
			run:      models.WorkflowRun{Status: "in_progress"},
			expected: "in_progress",
		},
		{
			name:     "queued shows status",
			run:      models.WorkflowRun{Status: "queued"},
			expected: "queued",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := tt.run.StatusDisplay()
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestWorkflowSummaryStatusDisplay(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		summary  models.WorkflowSummary
		expected string
	}{
		{
			name:     "no runs",
			summary:  models.WorkflowSummary{Total: 0},
			expected: models.EmDash,
		},
		{
			name:     "all passing",
			summary:  models.WorkflowSummary{Total: 2, Passing: 2},
			expected: "passing",
		},
		{
			name:     "has failures",
			summary:  models.WorkflowSummary{Total: 2, Passing: 1, Failing: 1},
			expected: "failing",
		},
		{
			name:     "in progress",
			summary:  models.WorkflowSummary{Total: 2, Passing: 1, InProgress: 1},
			expected: "running",
		},
		{
			name:     "mixed",
			summary:  models.WorkflowSummary{Total: 3, Passing: 2},
			expected: "mixed",
		},
		{
			name:     "failing takes priority",
			summary:  models.WorkflowSummary{Total: 3, Failing: 1, InProgress: 2},
			expected: "failing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := tt.summary.StatusDisplay()
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestCheckDetailStatusDisplay(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		check    models.CheckDetail
		expected string
	}{
		{"completed uses the conclusion", models.CheckDetail{Status: "COMPLETED", Conclusion: "SUCCESS"}, "success"},
		{"in flight uses the status", models.CheckDetail{Status: "IN_PROGRESS"}, "in progress"},
		{"queued", models.CheckDetail{Status: "QUEUED"}, "queued"},
		{"unknown", models.CheckDetail{}, "—"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.check.StatusDisplay(); got != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, got)
			}
		})
	}
}

func TestCheckDetailDuration(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	completed := models.CheckDetail{StartedAt: start, CompletedAt: start.Add(90 * time.Second)}
	if got := completed.Duration(); got != "1m30s" {
		t.Errorf("expected '1m30s', got %q", got)
	}

	running := models.CheckDetail{StartedAt: start}
	if got := running.Duration(); got != "—" {
		t.Errorf("expected an em dash while running, got %q", got)
	}
}
