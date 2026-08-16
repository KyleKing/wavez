package models

import (
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

// PeerCheckout is another working directory holding the same remote repository:
// a sibling clone discovered in the same scan, or a git worktree / jj workspace.
type PeerCheckout struct {
	Path       string
	Branch     string
	Ahead      int
	Behind     int
	LastCommit time.Time
	Dirty      bool
	IsWorktree bool
	IsLocked   bool
}

// Kind reports whether the checkout is a sibling clone or a worktree. It
// describes the checkout itself, not the vantage point it is listed from, so a
// worktree reads the same whether its parent or an unrelated clone names it.
func (p *PeerCheckout) Kind() string {
	if p.IsWorktree {
		return "worktree"
	}

	return "clone"
}

// Folder returns the checkout's directory name.
func (p *PeerCheckout) Folder() string {
	return filepath.Base(p.Path)
}

// TrackingSummary renders the checkout's ahead/behind counts, or a checkmark when in sync.
func (p *PeerCheckout) TrackingSummary() string {
	summary := ""
	if p.Ahead > 0 {
		summary += "↑" + strconv.Itoa(p.Ahead)
	}
	if p.Behind > 0 {
		if summary != "" {
			summary += " "
		}
		summary += "↓" + strconv.Itoa(p.Behind)
	}
	if summary == "" {
		return "✓"
	}

	return summary
}

// FindPeerCheckouts returns the other discovered repos sharing current's remote,
// sorted by folder name. Repos without a known remote never peer with anything,
// since an empty remote would otherwise group every unrelated local-only repo.
func FindPeerCheckouts(current *RepoSummary, all []RepoSummary) []PeerCheckout {
	if current == nil || current.RemoteRepo == "" {
		return nil
	}

	var peers []PeerCheckout
	for i := range all {
		summary := &all[i]
		if summary.Path == current.Path || summary.RemoteRepo != current.RemoteRepo {
			continue
		}

		peers = append(peers, OwnCheckout(summary))
	}

	sortCheckouts(peers)

	return peers
}

// OwnCheckout describes a repo's own working directory as a checkout, so it
// can be compared against its peers on the same footing.
func OwnCheckout(s *RepoSummary) PeerCheckout {
	return PeerCheckout{
		Path:       s.Path,
		Branch:     s.Branch,
		Ahead:      s.Ahead,
		Behind:     s.Behind,
		LastCommit: s.LastModified,
		Dirty:      s.UncommittedCount() > 0,
		IsWorktree: s.IsLinkedCheckout(),
	}
}

// WorktreeCheckouts converts a repo's worktree list into peer checkouts,
// dropping the repo's own working directory and any bare entry.
func WorktreeCheckouts(repoPath string, worktrees []WorktreeInfo) []PeerCheckout {
	var peers []PeerCheckout
	for _, wt := range worktrees {
		if wt.IsBare || wt.Path == "" || wt.Path == repoPath {
			continue
		}

		peers = append(peers, PeerCheckout{
			Path:       wt.Path,
			Branch:     wt.Branch,
			IsWorktree: true,
			IsLocked:   wt.IsLocked,
		})
	}

	sortCheckouts(peers)

	return peers
}

// MergeCheckouts concatenates checkout lists, keeping the first entry's
// tracking data for any repeated path so a sibling clone's richer counts win
// over a sparse worktree entry. The worktree and lock flags still carry over,
// because a directory discovered as its own clone is a worktree all the same.
func MergeCheckouts(lists ...[]PeerCheckout) []PeerCheckout {
	index := make(map[string]int)

	var merged []PeerCheckout
	for _, list := range lists {
		for _, checkout := range list {
			at, seen := index[checkout.Path]
			if !seen {
				index[checkout.Path] = len(merged)
				merged = append(merged, checkout)

				continue
			}

			merged[at].IsWorktree = merged[at].IsWorktree || checkout.IsWorktree
			merged[at].IsLocked = merged[at].IsLocked || checkout.IsLocked
		}
	}

	sortCheckouts(merged)

	return merged
}

// CheckoutForBranch returns the peer checkout that has branch checked out, if any.
func CheckoutForBranch(peers []PeerCheckout, branch string) (PeerCheckout, bool) {
	if branch == "" {
		return PeerCheckout{}, false
	}

	for _, peer := range peers {
		if peer.Branch == branch {
			return peer, true
		}
	}

	return PeerCheckout{}, false
}

// ConflictingBranches returns the branches held by more than one checkout of
// the same repo, counting the repo's own. Two checkouts on one branch is the
// state that silently loses local commits, so it is worth flagging wherever a
// checkout is named.
//
// The default branch is the exception: backup clones and idle worktrees park
// on main, and flagging every one of them buries the real signal. It counts
// only once some checkout of it has work the others cannot see.
func ConflictingBranches(own *PeerCheckout, peers []PeerCheckout) map[string]bool {
	all := make([]PeerCheckout, 0, len(peers)+1)
	if own != nil && own.Branch != "" {
		all = append(all, *own)
	}
	for _, peer := range peers {
		if peer.Branch != "" {
			all = append(all, peer)
		}
	}

	counts := make(map[string]int, len(all))
	unsynced := make(map[string]bool, len(all))
	for _, checkout := range all {
		counts[checkout.Branch]++
		if checkout.Dirty || checkout.Ahead > 0 {
			unsynced[checkout.Branch] = true
		}
	}

	conflicts := make(map[string]bool)
	for branch, count := range counts {
		if count > 1 && (unsynced[branch] || !IsDefaultBranchName(branch)) {
			conflicts[branch] = true
		}
	}

	return conflicts
}

func sortCheckouts(peers []PeerCheckout) {
	sort.Slice(peers, func(i, j int) bool { return peers[i].Folder() < peers[j].Folder() })
}
