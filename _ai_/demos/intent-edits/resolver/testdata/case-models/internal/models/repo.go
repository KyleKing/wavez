package models

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// RepoSummary is the top-level per-repo state shown in the repo list.
type RepoSummary struct {
	Path         string
	VCSType      VCSType
	Branch       string
	Upstream     string
	Ahead        int
	Behind       int
	Staged       int
	Unstaged     int
	Untracked    int
	Conflicted   int
	StashCount   int
	LastModified time.Time
	PRInfo       *PRInfo
	WorkflowInfo *WorkflowSummary
	TemplateInfo *CopierTemplateInfo
	Loading      bool
	Error        error

	NotesFiles      []NoteFile
	RemoteProtocol  string // "ssh", "https", or "" if unknown/no remote
	RemoteRepo      string // "owner/repo" derived from the remote URL, "" if unknown/no remote
	ConfigOverrides []GitConfigOverride
	// ParentPath is the repo this one borrows its refs from, set only for a
	// git worktree or jj workspace. It makes a linked checkout recognizable
	// from anywhere, not just from the parent that listed it.
	ParentPath string
	// NoCommits marks a repo whose branch has no commits yet, which has no
	// upstream for a different reason than a branch that was never pushed.
	NoCommits bool

	// RemoteID is the cache identity of the remote, host/owner/repo lowercased, and empty when there is no resolvable remote.
	RemoteID string
}

// IsDetached reports whether the repo's HEAD points at a commit rather than a
// branch.
func (r RepoSummary) IsDetached() bool {
	return IsDetachedBranch(r.Branch)
}

// DirtyLabel names why IsDirty is true, since uncommitted files and unpushed
// commits need different work to resolve. Empty when the repo is neither.
func (r RepoSummary) DirtyLabel() string {
	switch {
	case r.UncommittedCount() > 0 && r.Ahead > 0:
		return "uncommitted, unpushed"
	case r.UncommittedCount() > 0:
		return "uncommitted"
	case r.Ahead > 0:
		return "unpushed"
	default:
		return ""
	}
}

// IsLinkedCheckout reports whether the repo is a git worktree or jj workspace
// of another checkout rather than a standalone clone.
func (r RepoSummary) IsLinkedCheckout() bool {
	return r.ParentPath != ""
}

// HasNotes reports whether any notes file was detected at the repo's root.
func (r RepoSummary) HasNotes() bool {
	return len(r.NotesFiles) > 0
}

// HasConfigOverrides reports whether the repo has any local git config value
// that differs from the same key's global value.
func (r RepoSummary) HasConfigOverrides() bool {
	return len(r.ConfigOverrides) > 0
}

// Name returns the repo's directory name.
func (r RepoSummary) Name() string {
	return filepath.Base(r.Path)
}

// UncommittedCount returns the total number of staged, unstaged, untracked, and conflicted files.
func (r RepoSummary) UncommittedCount() int {
	return r.Staged + r.Unstaged + r.Untracked + r.Conflicted
}

// IsDirty reports whether the repo has uncommitted changes or unpushed commits.
func (r RepoSummary) IsDirty() bool {
	return r.UncommittedCount() > 0 || r.Ahead > 0
}

// Status returns the repo's overall RepoStatus.
func (r RepoSummary) Status() RepoStatus {
	if r.Ahead > 0 && r.Behind > 0 {
		return RepoStatusDiverged
	}
	if r.Ahead > 0 {
		return RepoStatusAhead
	}
	if r.Behind > 0 {
		return RepoStatusBehind
	}
	if r.UncommittedCount() > 0 {
		return RepoStatusDirty
	}

	return RepoStatusClean
}

// StatusSummary renders a compact symbol-based summary of the repo's working tree state.
func (r RepoSummary) StatusSummary() string {
	parts := []string{}

	if r.Staged > 0 {
		parts = append(parts, fmt.Sprintf("+%d", r.Staged))
	}
	if r.Unstaged > 0 {
		parts = append(parts, fmt.Sprintf("~%d", r.Unstaged))
	}
	if r.Untracked > 0 {
		parts = append(parts, fmt.Sprintf("?%d", r.Untracked))
	}
	if r.Conflicted > 0 {
		parts = append(parts, fmt.Sprintf("!%d", r.Conflicted))
	}
	if r.Ahead > 0 {
		parts = append(parts, fmt.Sprintf("↑%d", r.Ahead))
	}
	if r.Behind > 0 {
		parts = append(parts, fmt.Sprintf("↓%d", r.Behind))
	}

	if len(parts) == 0 {
		return "✓"
	}

	return strings.Join(parts, " ")
}

// RelativeModified returns a human-readable relative time for the repo's last modification.
func (r RepoSummary) RelativeModified() string {
	if r.LastModified.IsZero() {
		return emDash
	}

	return RelativeTime(r.LastModified)
}

// WorktreeInfo summarizes a single git worktree.
type WorktreeInfo struct {
	Path     string
	Branch   string
	IsBare   bool
	IsLocked bool
}
