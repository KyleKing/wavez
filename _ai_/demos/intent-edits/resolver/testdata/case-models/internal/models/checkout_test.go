package models_test

import (
	"testing"

	"github.com/kyleking/gh-repo-dashboard/internal/models"
)

const mainBranch = "main"

func TestFindPeerCheckouts(t *testing.T) {
	t.Parallel()

	primary := models.RepoSummary{Path: "/src/app", Branch: mainBranch, RemoteRepo: "acme/app"}
	sibling := models.RepoSummary{
		Path: "/src/app-feature", Branch: "feature", RemoteRepo: "acme/app", Ahead: 2, Behind: 1, Unstaged: 3,
	}
	other := models.RepoSummary{Path: "/src/tool", Branch: mainBranch, RemoteRepo: "acme/tool"}
	localOnly := models.RepoSummary{Path: "/src/scratch", Branch: mainBranch}

	tests := []struct {
		name    string
		current models.RepoSummary
		all     []models.RepoSummary
		want    []string
	}{
		{
			name:    "sibling sharing a remote",
			current: primary,
			all:     []models.RepoSummary{primary, sibling, other},
			want:    []string{"app-feature"},
		},
		{
			name:    "different remotes never peer",
			current: other,
			all:     []models.RepoSummary{primary, sibling, other},
			want:    nil,
		},
		{
			name:    "repos without a remote never peer",
			current: localOnly,
			all:     []models.RepoSummary{localOnly, {Path: "/src/scratch2", Branch: mainBranch}},
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			peers := models.FindPeerCheckouts(&tt.current, tt.all)
			if len(peers) != len(tt.want) {
				t.Fatalf("expected %d peers, got %d", len(tt.want), len(peers))
			}
			for i, folder := range tt.want {
				if peers[i].Folder() != folder {
					t.Errorf("peer %d: expected %q, got %q", i, folder, peers[i].Folder())
				}
			}
		})
	}
}

func TestFindPeerCheckoutsCarriesTracking(t *testing.T) {
	t.Parallel()

	current := models.RepoSummary{Path: "/src/app", RemoteRepo: "acme/app"}
	sibling := models.RepoSummary{
		Path: "/src/app-feature", Branch: "feature", RemoteRepo: "acme/app", Ahead: 2, Behind: 1, Unstaged: 3,
	}

	peers := models.FindPeerCheckouts(&current, []models.RepoSummary{current, sibling})
	if len(peers) != 1 {
		t.Fatalf("expected 1 peer, got %d", len(peers))
	}

	peer := peers[0]
	if peer.Branch != "feature" || peer.Ahead != 2 || peer.Behind != 1 || !peer.Dirty {
		t.Errorf("unexpected peer: %+v", peer)
	}
	if peer.TrackingSummary() != "↑2 ↓1" {
		t.Errorf("expected '↑2 ↓1', got %q", peer.TrackingSummary())
	}
}

func TestTrackingSummaryInSync(t *testing.T) {
	t.Parallel()
	empty := models.PeerCheckout{}
	if got := empty.TrackingSummary(); got != "✓" {
		t.Errorf("expected '✓', got %q", got)
	}
}

func TestWorktreeCheckouts(t *testing.T) {
	t.Parallel()

	worktrees := []models.WorktreeInfo{
		{Path: "/src/app", Branch: mainBranch},
		{Path: "/src/app-wt", Branch: "feature"},
		{Path: "/src/app-bare", IsBare: true},
	}

	checkouts := models.WorktreeCheckouts("/src/app", worktrees)
	if len(checkouts) != 1 {
		t.Fatalf("expected 1 checkout, got %d", len(checkouts))
	}
	if checkouts[0].Folder() != "app-wt" || !checkouts[0].IsWorktree {
		t.Errorf("unexpected checkout: %+v", checkouts[0])
	}
}

func TestMergeCheckoutsPrefersFirstEntryPerPath(t *testing.T) {
	t.Parallel()

	clones := []models.PeerCheckout{{Path: "/src/app-wt", Branch: "feature", Ahead: 3}}
	worktrees := []models.PeerCheckout{{Path: "/src/app-wt", Branch: "feature", IsWorktree: true}}

	merged := models.MergeCheckouts(clones, worktrees)
	if len(merged) != 1 {
		t.Fatalf("expected 1 checkout, got %d", len(merged))
	}
	if merged[0].Ahead != 3 {
		t.Errorf("expected the clone entry to win, got %+v", merged[0])
	}
}

func TestCheckoutForBranch(t *testing.T) {
	t.Parallel()

	peers := []models.PeerCheckout{
		{Path: "/src/app-wt", Branch: "feature"},
		{Path: "/src/app-old", Branch: mainBranch},
	}

	if peer, ok := models.CheckoutForBranch(peers, "feature"); !ok || peer.Folder() != "app-wt" {
		t.Errorf("expected app-wt for 'feature', got %+v (ok=%v)", peer, ok)
	}
	if _, ok := models.CheckoutForBranch(peers, "missing"); ok {
		t.Error("expected no checkout for an unheld branch")
	}
	if _, ok := models.CheckoutForBranch(peers, ""); ok {
		t.Error("expected no checkout for an empty branch name")
	}
}
