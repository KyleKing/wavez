package vcs

import (
	"context"

	"github.com/kyleking/gh-repo-dashboard/internal/models"
)

// MockOperations is a test double implementing Operations via injectable function fields.
type MockOperations struct {
	GetRepoSummaryFn        func(ctx context.Context, repoPath string) (models.RepoSummary, error)
	GetCurrentBranchFn      func(ctx context.Context, repoPath string) (string, error)
	GetUpstreamFn           func(ctx context.Context, repoPath, branch string) (string, error)
	GetAheadBehindFn        func(ctx context.Context, repoPath, branch, upstream string) (int, int, error)
	CompareBranchesFn       func(ctx context.Context, repoPath, branch, target string) (int, int, error)
	GetBranchListFn         func(ctx context.Context, repoPath string) ([]models.BranchInfo, error)
	GetStashListFn          func(ctx context.Context, repoPath string) ([]models.StashDetail, error)
	GetWorktreeListFn       func(ctx context.Context, repoPath string) ([]models.WorktreeInfo, error)
	GetCommitLogFn          func(ctx context.Context, repoPath string, count int) ([]models.CommitInfo, error)
	GetLastModifiedFn       func(ctx context.Context, repoPath string) (int64, error)
	GetRemoteURLFn          func(ctx context.Context, repoPath string) (string, error)
	VCSTypeFn               func() models.VCSType
	FetchAllFn              func(ctx context.Context, repoPath string) (bool, string, error)
	PruneRemoteFn           func(ctx context.Context, repoPath string) (bool, string, error)
	PushBranchFn            func(ctx context.Context, repoPath, branch string, setUpstream bool) (bool, string, error)
	SwitchBranchFn          func(ctx context.Context, repoPath, branch string) (bool, string, error)
	CleanupMergedBranchesFn func(ctx context.Context, repoPath string, squashMerged []string) (bool, string, error)
}

// GetRepoSummary implements Operations.
func (m *MockOperations) GetRepoSummary(ctx context.Context, repoPath string) (models.RepoSummary, error) {
	if m.GetRepoSummaryFn != nil {
		return m.GetRepoSummaryFn(ctx, repoPath)
	}

	return models.RepoSummary{Path: repoPath}, nil
}

// GetCurrentBranch implements Operations.
func (m *MockOperations) GetCurrentBranch(ctx context.Context, repoPath string) (string, error) {
	if m.GetCurrentBranchFn != nil {
		return m.GetCurrentBranchFn(ctx, repoPath)
	}

	return "main", nil
}

// GetUpstream implements Operations.
func (m *MockOperations) GetUpstream(ctx context.Context, repoPath, branch string) (string, error) {
	if m.GetUpstreamFn != nil {
		return m.GetUpstreamFn(ctx, repoPath, branch)
	}

	return "", nil
}

// GetAheadBehind implements Operations.
//
//nolint:gocritic // matches the Operations interface's (ahead, behind int, err error)
func (m *MockOperations) GetAheadBehind(ctx context.Context, repoPath, branch, upstream string) (int, int, error) {
	if m.GetAheadBehindFn != nil {
		return m.GetAheadBehindFn(ctx, repoPath, branch, upstream)
	}

	return 0, 0, nil
}

// CompareBranches implements Operations.
//
//nolint:gocritic // matches the Operations interface's (ahead, behind int, err error)
func (m *MockOperations) CompareBranches(ctx context.Context, repoPath, branch, target string) (int, int, error) {
	if m.CompareBranchesFn != nil {
		return m.CompareBranchesFn(ctx, repoPath, branch, target)
	}

	return 0, 0, nil
}

// GetBranchList implements Operations.
func (m *MockOperations) GetBranchList(ctx context.Context, repoPath string) ([]models.BranchInfo, error) {
	if m.GetBranchListFn != nil {
		return m.GetBranchListFn(ctx, repoPath)
	}

	return nil, nil
}

// GetStashList implements Operations.
func (m *MockOperations) GetStashList(ctx context.Context, repoPath string) ([]models.StashDetail, error) {
	if m.GetStashListFn != nil {
		return m.GetStashListFn(ctx, repoPath)
	}

	return nil, nil
}

// GetWorktreeList implements Operations.
func (m *MockOperations) GetWorktreeList(ctx context.Context, repoPath string) ([]models.WorktreeInfo, error) {
	if m.GetWorktreeListFn != nil {
		return m.GetWorktreeListFn(ctx, repoPath)
	}

	return nil, nil
}

// GetCommitLog implements Operations.
func (m *MockOperations) GetCommitLog(ctx context.Context, repoPath string, count int) ([]models.CommitInfo, error) {
	if m.GetCommitLogFn != nil {
		return m.GetCommitLogFn(ctx, repoPath, count)
	}

	return nil, nil
}

// GetLastModified implements Operations.
func (m *MockOperations) GetLastModified(ctx context.Context, repoPath string) (int64, error) {
	if m.GetLastModifiedFn != nil {
		return m.GetLastModifiedFn(ctx, repoPath)
	}

	return 0, nil
}

// GetRemoteURL implements Operations.
func (m *MockOperations) GetRemoteURL(ctx context.Context, repoPath string) (string, error) {
	if m.GetRemoteURLFn != nil {
		return m.GetRemoteURLFn(ctx, repoPath)
	}

	return "", nil
}

// VCSType implements Operations.
func (m *MockOperations) VCSType() models.VCSType {
	if m.VCSTypeFn != nil {
		return m.VCSTypeFn()
	}

	return models.VCSTypeGit
}

// FetchAll implements Operations.
//
//nolint:gocritic // matches the Operations interface's (ok bool, msg string, err error)
func (m *MockOperations) FetchAll(ctx context.Context, repoPath string) (bool, string, error) {
	if m.FetchAllFn != nil {
		return m.FetchAllFn(ctx, repoPath)
	}

	return true, "Fetched", nil
}

// PruneRemote implements Operations.
//
//nolint:gocritic // matches the Operations interface's (ok bool, msg string, err error)
func (m *MockOperations) PruneRemote(ctx context.Context, repoPath string) (bool, string, error) {
	if m.PruneRemoteFn != nil {
		return m.PruneRemoteFn(ctx, repoPath)
	}

	return true, "Pruned", nil
}

// PushBranch implements Operations.
//
//nolint:gocritic // matches the Operations interface's (ok bool, msg string, err error)
func (m *MockOperations) PushBranch(
	ctx context.Context, repoPath, branch string, setUpstream bool,
) (bool, string, error) {
	if m.PushBranchFn != nil {
		return m.PushBranchFn(ctx, repoPath, branch, setUpstream)
	}

	return true, "Pushed", nil
}

// SwitchBranch implements Operations.
//
//nolint:gocritic // matches the Operations interface's (ok bool, msg string, err error)
func (m *MockOperations) SwitchBranch(ctx context.Context, repoPath, branch string) (bool, string, error) {
	if m.SwitchBranchFn != nil {
		return m.SwitchBranchFn(ctx, repoPath, branch)
	}

	return true, "Switched", nil
}

// CleanupMergedBranches implements Operations.
//
//nolint:gocritic // matches the Operations interface's (ok bool, msg string, err error)
func (m *MockOperations) CleanupMergedBranches(
	ctx context.Context, repoPath string, squashMerged []string,
) (bool, string, error) {
	if m.CleanupMergedBranchesFn != nil {
		return m.CleanupMergedBranchesFn(ctx, repoPath, squashMerged)
	}

	return true, "No branches to cleanup", nil
}

var _ Operations = (*MockOperations)(nil)
