package models

import (
	"strings"
	"time"
)

// Display values shared by PRInfo/ChecksStatus/WorkflowSummary and the views
// that render them, so both sides compare against the same constant.
const (
	PRStatusMerged         = "MERGED"
	PRStatusClosed         = "CLOSED"
	ReviewApproved         = "approved"
	ReviewChangesRequested = "changes requested"
	StatusCompleted        = "completed"
	StatusFailing          = "failing"
	StatusPassing          = "passing"
)

// PRInfo summarizes a pull request for the repo list and detail views.
type PRInfo struct {
	Number          int          `json:"number"`
	Title           string       `json:"title"`
	State           string       `json:"state"`
	URL             string       `json:"url"`
	IsDraft         bool         `json:"is_draft"`
	Mergeable       string       `json:"mergeable,omitempty"`
	HeadRef         string       `json:"head_ref"`
	HeadRepoOwner   string       `json:"head_repo_owner,omitempty"`
	BaseRef         string       `json:"base_ref"`
	Checks          ChecksStatus `json:"checks"`
	ReviewDecision  string       `json:"review_decision,omitempty"`
	ApprovedBy      []string     `json:"approved_by,omitempty"`
	ChangesRequests int          `json:"changes_requests,omitempty"`
	Activity        *PRActivity  `json:"activity,omitempty"`
}

// FromFork reports whether the pull request's head branch lives in someone
// else's fork rather than in owner's own repository. A fork's head ref shares
// a namespace with local branches ("master" is common), so a name match alone
// is not evidence the branch is here.
func (p PRInfo) FromFork(owner string) bool {
	return p.HeadRepoOwner != "" && owner != "" && !strings.EqualFold(p.HeadRepoOwner, owner)
}

// HeadLabel names where the head branch lives, qualifying it with the owner
// when the pull request comes from a fork.
func (p PRInfo) HeadLabel(owner string) string {
	if p.FromFork(owner) {
		return p.HeadRepoOwner + ":" + p.HeadRef
	}

	return p.HeadRef
}

// PRActivity is the most recent comment or review on a pull request: the
// signal for who a pull request is waiting on.
type PRActivity struct {
	Author string    `json:"author"`
	At     time.Time `json:"at"`
}

// ActivitySummary renders the latest activity as an age and an author, or
// emDash when the pull request has neither comments nor reviews.
func (p *PRInfo) ActivitySummary() string {
	if p.Activity == nil || p.Activity.At.IsZero() {
		return emDash
	}

	return RelativeTime(p.Activity.At) + " " + p.Activity.Author
}

// ReviewGlyph marks an approval or a change request, or is empty otherwise.
func (p *PRInfo) ReviewGlyph() string {
	switch p.ReviewDecision {
	case "APPROVED":
		return "✓"
	case "CHANGES_REQUESTED":
		return "✗"
	default:
		return ""
	}
}

// StatusDisplay returns the pull request's display status label.
func (p PRInfo) StatusDisplay() string {
	if p.IsDraft {
		return "DRAFT"
	}
	switch p.State {
	case "OPEN":
		return "OPEN"
	case PRStatusMerged:
		return PRStatusMerged
	case PRStatusClosed:
		return PRStatusClosed
	default:
		return p.State
	}
}

// ReviewStatus returns a human-readable summary of the pull request's review decision.
func (p PRInfo) ReviewStatus() string {
	switch p.ReviewDecision {
	case "APPROVED":
		return ReviewApproved
	case "CHANGES_REQUESTED":
		return ReviewChangesRequested
	case "REVIEW_REQUIRED":
		return "review required"
	default:
		if len(p.ApprovedBy) > 0 {
			return ReviewApproved
		}

		return emDash
	}
}

// ChecksStatus tallies a pull request's CI check outcomes.
type ChecksStatus struct {
	Total   int `json:"total"`
	Passing int `json:"passing"`
	Failing int `json:"failing"`
	Pending int `json:"pending"`
	Skipped int `json:"skipped"`
}

// Summary returns a one-word overall status for the checks.
func (c ChecksStatus) Summary() string {
	if c.Total == 0 {
		return emDash
	}
	if c.Failing > 0 {
		return StatusFailing
	}
	if c.Pending > 0 {
		return "pending"
	}
	if c.Passing == c.Total {
		return StatusPassing
	}

	return "mixed"
}

// CheckDetail is a single CI check on a pull request.
type CheckDetail struct {
	Name        string
	Workflow    string
	Status      string
	Conclusion  string
	StartedAt   time.Time
	CompletedAt time.Time
}

// StatusDisplay returns the check's lowercased conclusion once it has
// completed, or its in-flight status ("queued", "in progress") before then.
func (c CheckDetail) StatusDisplay() string {
	status := strings.ToLower(c.Status)
	if status == StatusCompleted && c.Conclusion != "" {
		return strings.ToLower(c.Conclusion)
	}
	if status == "" {
		return emDash
	}

	return strings.ReplaceAll(status, "_", " ")
}

// Duration renders how long the check ran, or emDash while it's still running.
func (c CheckDetail) Duration() string {
	if c.StartedAt.IsZero() || c.CompletedAt.IsZero() {
		return emDash
	}

	elapsed := c.CompletedAt.Sub(c.StartedAt).Round(time.Second)
	if elapsed < 0 {
		return emDash
	}

	return elapsed.String()
}

// PRComment is a single issue comment on a pull request.
type PRComment struct {
	Author    string
	Body      string
	CreatedAt time.Time
}

// RelativeCreated returns a human-readable relative time for the comment.
func (c PRComment) RelativeCreated() string {
	return RelativeTime(c.CreatedAt)
}

// PRDetail holds the full detail view state for a single pull request.
type PRDetail struct {
	PRInfo
	Body      string
	Author    string
	Assignees []string
	Reviewers []string
	CreatedAt time.Time
	UpdatedAt time.Time
	Additions int
	Deletions int
	// Comments counts issue comments on the pull request; LatestComment is
	// the most recent of them, nil when there are none.
	Comments      int
	LatestComment *PRComment
	CheckDetails  []CheckDetail
	ReviewsURL    string
}

// RelativeCreated returns a human-readable relative time for the pull request's creation.
func (p PRDetail) RelativeCreated() string {
	return RelativeTime(p.CreatedAt)
}

// RelativeUpdated returns a human-readable relative time for the pull request's last update.
func (p PRDetail) RelativeUpdated() string {
	return RelativeTime(p.UpdatedAt)
}

// WorkflowRun summarizes a single CI workflow run.
type WorkflowRun struct {
	ID         int64
	Name       string
	Status     string
	Conclusion string
	URL        string
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// StatusDisplay returns the workflow run's display status label.
func (w WorkflowRun) StatusDisplay() string {
	if w.Status == StatusCompleted {
		return w.Conclusion
	}

	return w.Status
}

// DefaultBranchCI is the CI state of a repo's default branch head: the latest
// run of each workflow on that commit.
type DefaultBranchCI struct {
	Branch    string          `json:"branch"`
	SHA       string          `json:"sha"`
	Workflows []CIWorkflowRun `json:"workflows"`
}

// Conclusion rolls the workflows up into one word: failing if any failed,
// pending while any is still going, passing when all succeeded, and emDash
// when the commit has no runs at all.
func (c *DefaultBranchCI) Conclusion() string {
	if len(c.Workflows) == 0 {
		return emDash
	}

	pending := false
	for i := range c.Workflows {
		switch {
		case c.Workflows[i].Conclusion == "failure":
			return StatusFailing
		case c.Workflows[i].Status != StatusCompleted:
			pending = true
		}
	}

	if pending {
		return "pending"
	}

	return StatusPassing
}

// CIWorkflowRun is one workflow's latest run on a commit.
type CIWorkflowRun struct {
	ID          int64     `json:"id"`
	Workflow    string    `json:"workflow"`
	Status      string    `json:"status"`
	Conclusion  string    `json:"conclusion"`
	URL         string    `json:"url"`
	StartedAt   time.Time `json:"started_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	FailingJobs []string  `json:"failing_jobs,omitempty"`
}

// WorkflowSummary aggregates the CI workflow runs for a commit.
type WorkflowSummary struct {
	Runs       []WorkflowRun
	Total      int
	Passing    int
	Failing    int
	InProgress int
}

// StatusDisplay returns a one-word overall status for the workflow runs.
func (w WorkflowSummary) StatusDisplay() string {
	if w.Total == 0 {
		return emDash
	}
	if w.Failing > 0 {
		return StatusFailing
	}
	if w.InProgress > 0 {
		return "running"
	}
	if w.Passing == w.Total {
		return StatusPassing
	}

	return "mixed"
}
