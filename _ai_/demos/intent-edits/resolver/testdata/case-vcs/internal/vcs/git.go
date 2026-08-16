package vcs

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kyleking/gh-repo-dashboard/internal/models"
)

const (
	porcelainStatusCodeLen = 2
	minRemoteURLPathParts  = 3
)

// GitOperations implements Operations for git repositories.
type GitOperations struct{}

// NewGitOperations returns a GitOperations.
func NewGitOperations() *GitOperations {
	return &GitOperations{}
}

// VCSType implements Operations.
func (*GitOperations) VCSType() models.VCSType {
	return models.VCSTypeGit
}

func (*GitOperations) runGit(ctx context.Context, repoPath string, args ...string) (string, error) {
	out, err := runCommand(ctx, repoPath, "git", args...)
	if err != nil {
		return "", gitError(args, err)
	}

	return out, nil
}

// runGitRaw runs git without trimming its output, for NUL-delimited formats
// whose first record can legitimately start with a space.
func (*GitOperations) runGitRaw(ctx context.Context, repoPath string, args ...string) (string, error) {
	out, err := runCommandRaw(ctx, repoPath, "git", args...)
	if err != nil {
		return "", gitError(args, err)
	}

	return out, nil
}

func gitError(args []string, err error) error {
	exitErr := &exec.ExitError{}
	if errors.As(err, &exitErr) {
		return fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), string(exitErr.Stderr), ErrCommandFailed)
	}

	return err
}

// GetRepoSummary implements Operations.
func (g *GitOperations) GetRepoSummary(ctx context.Context, repoPath string) (models.RepoSummary, error) {
	summary := models.RepoSummary{
		Path:    repoPath,
		VCSType: models.VCSTypeGit,
	}

	branch, err := g.GetCurrentBranch(ctx, repoPath)
	if err != nil {
		// A repo with no commits has a branch but no revision to resolve, so
		// rev-parse fails where symbolic-ref still answers.
		unborn, refErr := g.runGit(ctx, repoPath, "symbolic-ref", "--short", "HEAD")
		if refErr != nil {
			return summary, err
		}

		summary.Branch = unborn
		summary.NoCommits = true

		return summary, nil
	}
	summary.Branch = branch

	// The remaining fields are best-effort: a failure on any one of them
	// shouldn't blank out an otherwise-populated summary.
	upstream, _ := g.GetUpstream(ctx, repoPath, branch) //nolint:errcheck // best-effort, see comment above
	summary.Upstream = upstream

	if upstream != "" {
		//nolint:errcheck // best-effort, see comment above
		ahead, behind, _ := g.GetAheadBehind(ctx, repoPath, branch, upstream)
		summary.Ahead = ahead
		summary.Behind = behind
	}

	counts := g.getStatusCounts(ctx, repoPath)
	summary.Staged = counts.staged
	summary.Unstaged = counts.unstaged
	summary.Untracked = counts.untracked
	summary.Conflicted = counts.conflicted

	stashCount, _ := g.getStashCount(ctx, repoPath) //nolint:errcheck // best-effort, see comment above
	summary.StashCount = stashCount

	lastMod, _ := g.GetLastModified(ctx, repoPath) //nolint:errcheck // best-effort, see comment above
	if lastMod > 0 {
		summary.LastModified = time.Unix(lastMod, 0)
	}

	remoteURL, _ := g.GetRemoteURL(ctx, repoPath) //nolint:errcheck // best-effort, see comment above
	summary.RemoteProtocol = detectRemoteProtocol(remoteURL)
	summary.RemoteRepo = ExtractRepoPath(remoteURL)
	summary.ConfigOverrides = g.getConfigOverrides(ctx, repoPath)
	summary.ParentPath = linkedParent(repoPath)

	return summary, nil
}

// GetCurrentBranch implements Operations.
func (g *GitOperations) GetCurrentBranch(ctx context.Context, repoPath string) (string, error) {
	out, err := g.runGit(ctx, repoPath, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	if out == "HEAD" {
		hash, err := g.runGit(ctx, repoPath, "rev-parse", "--short", "HEAD")
		if err != nil {
			//nolint:nilerr // degrade to plain "HEAD" label rather than failing the whole summary
			return "HEAD", nil
		}

		return models.DetachedBranchLabel(hash), nil
	}

	return out, nil
}

// GetUpstream implements Operations.
func (g *GitOperations) GetUpstream(ctx context.Context, repoPath, branch string) (string, error) {
	out, err := g.runGit(ctx, repoPath, "rev-parse", "--abbrev-ref", branch+"@{upstream}")
	if err != nil {
		return "", err
	}

	return out, nil
}

// GetAheadBehind implements Operations.
//
//nolint:gocritic // matches the Operations interface's (ahead, behind int, err error)
func (g *GitOperations) GetAheadBehind(ctx context.Context, repoPath, branch, upstream string) (int, int, error) {
	out, err := g.runGit(ctx, repoPath, "rev-list", "--left-right", "--count", fmt.Sprintf("%s...%s", branch, upstream))
	if err != nil {
		return 0, 0, err
	}

	const revListFieldCount = 2 // ahead, behind

	parts := strings.Fields(out)
	if len(parts) != revListFieldCount {
		return 0, 0, fmt.Errorf("rev-list output %q: %w", out, ErrUnexpectedOutput)
	}

	ahead, _ := strconv.Atoi(parts[0])  //nolint:errcheck // regex guarantees digits
	behind, _ := strconv.Atoi(parts[1]) //nolint:errcheck // regex guarantees digits

	return ahead, behind, nil
}

// CompareBranches implements Operations. Git's rev-list comparison works for
// any two local refs, so this reuses GetAheadBehind with the default branch as
// the right-hand side.
//
//nolint:gocritic // matches the Operations interface's (ahead, behind int, err error)
func (g *GitOperations) CompareBranches(ctx context.Context, repoPath, branch, target string) (int, int, error) {
	return g.GetAheadBehind(ctx, repoPath, branch, target)
}

type statusCounts struct {
	staged     int
	unstaged   int
	untracked  int
	conflicted int
}

// classifyPorcelainEntry categorizes one `git status --porcelain` entry by its
// two-character XY status code.
func classifyPorcelainEntry(x, y byte) statusCounts {
	switch {
	case x == 'U' || y == 'U' || (x == 'D' && y == 'D') || (x == 'A' && y == 'A'):
		return statusCounts{conflicted: 1}
	case x == '?':
		return statusCounts{untracked: 1}
	default:
		var counts statusCounts
		if x != ' ' && x != '?' {
			counts.staged = 1
		}
		if y != ' ' && y != '?' {
			counts.unstaged = 1
		}

		return counts
	}
}

// parsePorcelainZ tallies `git status --porcelain -z` records. The output must
// arrive untrimmed: an unstaged-only record starts with a space that is its
// staged status column. Rename and copy records are followed by a second
// NUL-terminated path, which is skipped rather than read as another record.
func parsePorcelainZ(out string) statusCounts {
	var counts statusCounts

	entries := strings.Split(out, "\x00")
	for i := 0; i < len(entries); i++ {
		entry := entries[i]
		if len(entry) < porcelainStatusCodeLen {
			continue
		}

		x, y := entry[0], entry[1]
		if x == 'R' || x == 'C' {
			i++
		}

		entryCounts := classifyPorcelainEntry(x, y)
		counts.staged += entryCounts.staged
		counts.unstaged += entryCounts.unstaged
		counts.untracked += entryCounts.untracked
		counts.conflicted += entryCounts.conflicted
	}

	return counts
}

func (g *GitOperations) getStatusCounts(ctx context.Context, repoPath string) statusCounts {
	out, err := g.runGitRaw(ctx, repoPath, "status", "--porcelain", "-z")
	if err != nil {
		return statusCounts{}
	}

	return parsePorcelainZ(out)
}

// GetStagedCount reports the number of staged files.
//
//nolint:unparam // error kept for signature parity with the other count methods exec tests exercise directly
func (g *GitOperations) GetStagedCount(ctx context.Context, repoPath string) (int, error) {
	return g.getStatusCounts(ctx, repoPath).staged, nil
}

// GetUnstagedCount reports the number of unstaged, modified files.
//
//nolint:unparam // error kept for signature parity with the other count methods exec tests exercise directly
func (g *GitOperations) GetUnstagedCount(ctx context.Context, repoPath string) (int, error) {
	return g.getStatusCounts(ctx, repoPath).unstaged, nil
}

// GetUntrackedCount reports the number of untracked files.
//
//nolint:unparam // error kept for signature parity with the other count methods exec tests exercise directly
func (g *GitOperations) GetUntrackedCount(ctx context.Context, repoPath string) (int, error) {
	return g.getStatusCounts(ctx, repoPath).untracked, nil
}

// GetConflictedCount reports the number of files with merge conflicts.
//
//nolint:unparam // error kept for signature parity with the other count methods exec tests exercise directly
func (g *GitOperations) GetConflictedCount(ctx context.Context, repoPath string) (int, error) {
	return g.getStatusCounts(ctx, repoPath).conflicted, nil
}

func (g *GitOperations) getStashCount(ctx context.Context, repoPath string) (int, error) {
	out, err := g.runGit(ctx, repoPath, "stash", "list")
	if err != nil {
		return 0, err
	}
	if out == "" {
		return 0, nil
	}

	return len(strings.Split(out, "\n")), nil
}

// branchListFieldCount is the number of tab-separated fields in the
// for-each-ref format below (refname, upstream, track, date, HEAD marker,
// object name). RunCommand trims trailing whitespace from the output, so the
// final line can lose empty trailing fields (e.g. a last branch with no
// upstream); the parser pads missing fields back to this count.
const branchListFieldCount = 6

// GetBranchList implements Operations.
func (g *GitOperations) GetBranchList(ctx context.Context, repoPath string) ([]models.BranchInfo, error) {
	format := "%(refname:short)\t%(upstream:short)\t%(upstream:track)\t%(committerdate:unix)\t%(HEAD)\t%(objectname)"
	out, err := g.runGit(ctx, repoPath, "for-each-ref", "--format="+format, "refs/heads/")
	if err != nil {
		return nil, err
	}

	var branches []models.BranchInfo
	scanner := bufio.NewScanner(strings.NewReader(out))
	trackRe := regexp.MustCompile(`\[ahead (\d+)(?:, behind (\d+))?\]|\[behind (\d+)\]`)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		parts := strings.Split(line, "\t")
		for len(parts) < branchListFieldCount {
			parts = append(parts, "")
		}

		var ahead, behind int
		if matches := trackRe.FindStringSubmatch(parts[2]); matches != nil {
			if matches[1] != "" {
				ahead, _ = strconv.Atoi(matches[1]) //nolint:errcheck // regex guarantees digits
			}
			if matches[2] != "" {
				behind, _ = strconv.Atoi(matches[2]) //nolint:errcheck // regex guarantees digits
			}
			if matches[3] != "" {
				behind, _ = strconv.Atoi(matches[3]) //nolint:errcheck // regex guarantees digits
			}
		}

		ts, _ := strconv.ParseInt(parts[3], 10, 64) //nolint:errcheck // git emits a unix timestamp here

		branches = append(branches, models.BranchInfo{
			Name:       parts[0],
			Upstream:   parts[1],
			Ahead:      ahead,
			Behind:     behind,
			LastCommit: time.Unix(ts, 0),
			IsCurrent:  parts[4] == "*",
			Head:       parts[5],
		})
	}

	return branches, nil
}

// stashListFieldCount is the number of tab-separated fields in the
// stash-list format below (reflog short name, subject, date).
const stashListFieldCount = 3

// StashDiffstat summarizes what one stash changes, for the focused view's
// detail pane. Read-only and git-only, so it sits outside the Operations
// interface alongside PreviewMergedBranches.
func (g *GitOperations) StashDiffstat(ctx context.Context, repoPath string, index int) (string, error) {
	out, err := g.runGit(ctx, repoPath, "stash", "show", "--stat", "--no-color", fmt.Sprintf("stash@{%d}", index))
	if err != nil {
		return "", err
	}

	return out, nil
}

// GetStashList implements Operations.
func (g *GitOperations) GetStashList(ctx context.Context, repoPath string) ([]models.StashDetail, error) {
	format := "%gd\t%gs\t%ct"
	out, err := g.runGit(ctx, repoPath, "stash", "list", "--format="+format)
	if err != nil {
		return nil, err
	}

	if out == "" {
		return nil, nil
	}

	var stashes []models.StashDetail
	scanner := bufio.NewScanner(strings.NewReader(out))
	stashRe := regexp.MustCompile(`stash@\{(\d+)\}`)

	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Split(line, "\t")
		if len(parts) < stashListFieldCount {
			continue
		}

		var index int
		if matches := stashRe.FindStringSubmatch(parts[0]); matches != nil {
			index, _ = strconv.Atoi(matches[1]) //nolint:errcheck // regex guarantees digits
		}

		ts, _ := strconv.ParseInt(parts[2], 10, 64) //nolint:errcheck // git emits a unix timestamp here

		stashes = append(stashes, models.StashDetail{
			Index:   index,
			Message: parts[1],
			Date:    time.Unix(ts, 0),
		})
	}

	return stashes, nil
}

// GetWorktreeList implements Operations.
func (g *GitOperations) GetWorktreeList(ctx context.Context, repoPath string) ([]models.WorktreeInfo, error) {
	out, err := g.runGit(ctx, repoPath, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}

	var worktrees []models.WorktreeInfo
	var current models.WorktreeInfo

	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "worktree "):
			if current.Path != "" {
				worktrees = append(worktrees, current)
			}
			current = models.WorktreeInfo{Path: strings.TrimPrefix(line, "worktree ")}
		case strings.HasPrefix(line, "branch "):
			ref := strings.TrimPrefix(line, "branch ")
			current.Branch = strings.TrimPrefix(ref, "refs/heads/")
		case line == "bare":
			current.IsBare = true
		case line == "locked":
			current.IsLocked = true
		}
	}

	if current.Path != "" {
		worktrees = append(worktrees, current)
	}

	return worktrees, nil
}

// commitLogFieldCount is the number of tab-separated fields in the log
// format below (hash, short hash, subject, author, date).
const commitLogFieldCount = 5

// GetCommitLog implements Operations.
func (g *GitOperations) GetCommitLog(ctx context.Context, repoPath string, count int) ([]models.CommitInfo, error) {
	format := "%H\t%h\t%s\t%an\t%ct"
	out, err := g.runGit(ctx, repoPath, "log", fmt.Sprintf("-n%d", count), "--format="+format)
	if err != nil {
		return nil, err
	}

	var commits []models.CommitInfo
	scanner := bufio.NewScanner(strings.NewReader(out))

	for scanner.Scan() {
		parts := strings.Split(scanner.Text(), "\t")
		if len(parts) < commitLogFieldCount {
			continue
		}

		ts, _ := strconv.ParseInt(parts[4], 10, 64) //nolint:errcheck // git emits a unix timestamp here

		commits = append(commits, models.CommitInfo{
			Hash:      parts[0],
			ShortHash: parts[1],
			Subject:   parts[2],
			Author:    parts[3],
			Date:      time.Unix(ts, 0),
		})
	}

	return commits, nil
}

// GetLastModified implements Operations.
func (g *GitOperations) GetLastModified(ctx context.Context, repoPath string) (int64, error) {
	out, err := g.runGit(ctx, repoPath, "log", "-1", "--format=%ct")
	if err != nil {
		return 0, err
	}

	ts, err := strconv.ParseInt(out, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parsing commit timestamp: %w", err)
	}

	return ts, nil
}

// GetRemoteURL implements Operations.
func (g *GitOperations) GetRemoteURL(ctx context.Context, repoPath string) (string, error) {
	out, err := g.runGit(ctx, repoPath, "remote", "get-url", "origin")
	if err != nil {
		return "", err
	}

	return out, nil
}

// FetchAll implements Operations.
//
//nolint:gocritic // matches the Operations interface's (ok bool, msg string, err error)
func (g *GitOperations) FetchAll(ctx context.Context, repoPath string) (bool, string, error) {
	_, err := g.runGit(ctx, repoPath, "fetch", "--all", "--prune")
	if err != nil {
		//nolint:nilerr // failure is reported through the message, not the error field
		return false, err.Error(), nil
	}

	return true, "Fetched from all remotes", nil
}

// PushBranch implements Operations.
//
//nolint:gocritic // matches the Operations interface's (ok bool, msg string, err error)
func (g *GitOperations) PushBranch(
	ctx context.Context, repoPath, branch string, setUpstream bool,
) (bool, string, error) {
	args := []string{"push", "--follow-tags"}
	if setUpstream {
		args = append(args, "--set-upstream")
	}
	args = append(args, "origin", branch)

	if _, err := g.runGit(ctx, repoPath, args...); err != nil {
		//nolint:nilerr // failure is reported through the message, not the error field
		return false, err.Error(), nil
	}

	return true, "Pushed " + branch + " to origin", nil
}

// SwitchBranch implements Operations.
//
//nolint:gocritic // matches the Operations interface's (ok bool, msg string, err error)
func (g *GitOperations) SwitchBranch(ctx context.Context, repoPath, branch string) (bool, string, error) {
	if _, err := g.runGit(ctx, repoPath, "switch", branch); err != nil {
		//nolint:nilerr // failure is reported through the message, not the error field
		return false, err.Error(), nil
	}

	return true, "Switched to " + branch, nil
}

// PruneRemote implements Operations.
//
//nolint:gocritic // matches the Operations interface's (ok bool, msg string, err error)
func (g *GitOperations) PruneRemote(ctx context.Context, repoPath string) (bool, string, error) {
	_, err := g.runGit(ctx, repoPath, "remote", "prune", "origin")
	if err != nil {
		//nolint:nilerr // failure is reported through the message, not the error field
		return false, err.Error(), nil
	}

	return true, "Pruned stale remote branches", nil
}

// DeleteBranch implements Operations.
func (g *GitOperations) DeleteBranch(ctx context.Context, repoPath string) (bool, string, error) {
	// HOLE: mirror of PruneRemote
	_, err := g.runGit(ctx, repoPath, "remote", "prune", "origin")
	if err != nil {

		return false, err.Error(), nil
	}
	return true, "Pruned stale remote branches", nil
}

// resolveDefaultBranch returns the repository's default branch and whether one
// was found. It prefers the remote's advertised HEAD (`git symbolic-ref
// refs/remotes/origin/HEAD`), which reflects the actual default even when it's
// neither "main" nor "master", falling back to probing for local main/master
// when no such ref exists (e.g. no remote, or origin/HEAD was never set).
func (g *GitOperations) resolveDefaultBranch(ctx context.Context, repoPath string) (string, bool) {
	const originHEADPrefix = "refs/remotes/origin/"

	if out, err := g.runGit(ctx, repoPath, "symbolic-ref", "refs/remotes/origin/HEAD"); err == nil {
		if branch, ok := strings.CutPrefix(out, originHEADPrefix); ok && branch != "" {
			return branch, true
		}
	}

	if _, err := g.runGit(ctx, repoPath, "rev-parse", "--verify", defaultMainBranch); err == nil {
		return defaultMainBranch, true
	}
	if _, err := g.runGit(ctx, repoPath, "rev-parse", "--verify", masterBranch); err == nil {
		return masterBranch, true
	}

	return "", false
}

// PreviewMergedBranches reports the default branch and the local branches
// fully merged into it, without deleting anything. Used by the `:cleanup
// --dry-run` preview; not part of the Mutator interface since it's read-only.
//
//nolint:gocritic // matches JJOperations.PreviewMergedBranches's (default branch, merged, err)
func (g *GitOperations) PreviewMergedBranches(ctx context.Context, repoPath string) (string, []string, error) {
	mainBranch, ok := g.resolveDefaultBranch(ctx, repoPath)
	if !ok {
		return "", nil, nil
	}

	merged, err := g.mergedBranchNames(ctx, repoPath, mainBranch)
	if err != nil {
		return mainBranch, nil, err
	}

	return mainBranch, merged, nil
}

// mergedBranchNames lists deletable local branches fully merged into
// mainBranch, excluding mainBranch/master itself and anything checked out
// here or in a linked worktree. It reads refs directly rather than parsing
// `git branch --merged`, whose porcelain marks the current branch with "* ",
// worktree checkouts with "+ ", and a detached HEAD with a "(HEAD detached
// at …)" line that is no branch at all.
func (g *GitOperations) mergedBranchNames(ctx context.Context, repoPath, mainBranch string) ([]string, error) {
	out, err := g.runGit(ctx, repoPath,
		"for-each-ref", "--format=%(refname:short)", "--merged", mainBranch, "refs/heads")
	if err != nil {
		return nil, err
	}

	checkedOut := g.checkedOutBranches(ctx, repoPath)

	var names []string
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		branch := scanner.Text()

		if branch == "" || branch == mainBranch || IsDefaultBranchName(branch) || checkedOut[branch] {
			continue
		}

		names = append(names, branch)
	}

	return names, nil
}

// checkedOutBranches returns the branches git refuses to delete because they
// are checked out, here or in a linked worktree.
func (g *GitOperations) checkedOutBranches(ctx context.Context, repoPath string) map[string]bool {
	names := make(map[string]bool)

	if current, err := g.GetCurrentBranch(ctx, repoPath); err == nil && current != "" {
		names[current] = true
	}

	worktrees, err := g.GetWorktreeList(ctx, repoPath)
	if err != nil {
		return names
	}

	for _, wt := range worktrees {
		names[wt.Branch] = true
	}

	return names
}

// localBranchNames returns the set of local branch names, keyed for
// membership checks.
func (g *GitOperations) localBranchNames(ctx context.Context, repoPath string) (map[string]bool, error) {
	branches, err := g.GetBranchList(ctx, repoPath)
	if err != nil {
		return nil, err
	}

	names := make(map[string]bool, len(branches))
	for _, b := range branches {
		names[b.Name] = true
	}

	return names, nil
}

// deleteSquashMerged force-deletes branches in squashMerged that are local,
// not the current branch, and not checked out in any worktree. Squash-merged
// branches need `-D` because git's own merge-base check considers them
// unmerged (the squash commit differs from the branch's own tip).
//
//nolint:gocritic // matches CleanupMergedBranches's (deleted, failed []string)
func (g *GitOperations) deleteSquashMerged(
	ctx context.Context, repoPath string, squashMerged []string,
) ([]string, []string) {
	localBranches, _ := g.localBranchNames(ctx, repoPath) //nolint:errcheck // best-effort, see comment above
	checkedOut := g.checkedOutBranches(ctx, repoPath)

	var deleted, failed []string
	for _, branch := range squashMerged {
		if checkedOut[branch] || !localBranches[branch] {
			continue
		}

		if _, err := g.runGit(ctx, repoPath, "branch", "-D", branch); err != nil {
			failed = append(failed, fmt.Sprintf("%s (%s)", branch, err.Error()))
			continue
		}
		deleted = append(deleted, branch)
	}

	return deleted, failed
}

// cleanupMessage renders a human-readable summary of a cleanup run, naming
// both the branches deleted and any that failed to delete.
func cleanupMessage(noun string, deleted, failed []string) string {
	switch {
	case len(deleted) == 0 && len(failed) == 0:
		return "No merged " + noun + " to delete"
	case len(failed) == 0:
		return fmt.Sprintf("Deleted %d %s: %s", len(deleted), noun, strings.Join(deleted, ", "))
	case len(deleted) == 0:
		return fmt.Sprintf("Failed to delete %d %s: %s", len(failed), noun, strings.Join(failed, ", "))
	default:
		return fmt.Sprintf("Deleted %d %s: %s; failed: %s",
			len(deleted), noun, strings.Join(deleted, ", "), strings.Join(failed, ", "))
	}
}

// CleanupMergedBranches implements Operations. The squashMerged parameter
// names branches already verified by the caller (via merged PR head OIDs) as
// squash-merged: `git branch --merged` misses these because the squash
// commit differs from the original branch tip, so they're deleted with `-D`
// instead of `-d`, and only when not the current branch and not checked out
// in a worktree.
//
//nolint:gocritic // matches the Operations interface's (ok bool, msg string, err error)
func (g *GitOperations) CleanupMergedBranches(
	ctx context.Context, repoPath string, squashMerged []string,
) (bool, string, error) {
	mainBranch, ok := g.resolveDefaultBranch(ctx, repoPath)
	if !ok {
		return false, "Could not find main or master branch", nil
	}

	merged, err := g.mergedBranchNames(ctx, repoPath, mainBranch)
	if err != nil {
		//nolint:nilerr // failure is reported through the message, not the error field
		return false, err.Error(), nil
	}

	var deleted, failed []string
	for _, branch := range merged {
		if _, err := g.runGit(ctx, repoPath, "branch", "-d", branch); err != nil {
			failed = append(failed, fmt.Sprintf("%s (%s)", branch, err.Error()))
			continue
		}
		deleted = append(deleted, branch)
	}

	if len(squashMerged) > 0 {
		squashDeleted, squashFailed := g.deleteSquashMerged(ctx, repoPath, squashMerged)
		deleted = append(deleted, squashDeleted...)
		failed = append(failed, squashFailed...)
	}

	return len(failed) == 0, cleanupMessage("branches", deleted, failed), nil
}

// detectRemoteProtocol classifies a remote URL as "ssh" or "https", or ""
// when the URL is empty or in an unrecognized scheme.
func detectRemoteProtocol(remoteURL string) string {
	switch {
	case strings.HasPrefix(remoteURL, "git@"), strings.HasPrefix(remoteURL, "ssh://"):
		return "ssh"
	case strings.HasPrefix(remoteURL, "https://"), strings.HasPrefix(remoteURL, "http://"):
		return "https"
	default:
		return ""
	}
}

// parseConfigList parses `git config --list` output ("key=value" per line,
// multi-line values folded onto one line by git itself) into a map. Later
// duplicate keys win, matching git's own last-one-wins resolution.
func parseConfigList(out string) map[string]string {
	values := make(map[string]string)

	for _, line := range strings.Split(out, "\n") {
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		values[key] = value
	}

	return values
}

// getConfigOverrides reports git config keys whose repo-local value differs
// from that same key's global value. Keys set only locally (with no global
// counterpart, e.g. remote.origin.url) aren't overrides of anything and are
// excluded. Best-effort: either lookup failing yields no overrides rather
// than an error, consistent with the rest of GetRepoSummary.
func (g *GitOperations) getConfigOverrides(ctx context.Context, repoPath string) []models.GitConfigOverride {
	localOut, err := g.runGit(ctx, repoPath, "config", "--local", "--list")
	if err != nil {
		return nil
	}
	globalOut, err := g.runGit(ctx, repoPath, "config", "--global", "--list")
	if err != nil {
		return nil
	}

	local := parseConfigList(localOut)
	global := parseConfigList(globalOut)

	keys := make([]string, 0, len(local))
	for key := range local {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var overrides []models.GitConfigOverride
	for _, key := range keys {
		globalValue, hasGlobal := global[key]
		if hasGlobal && globalValue != local[key] {
			overrides = append(overrides, models.GitConfigOverride{
				Key:         key,
				LocalValue:  local[key],
				GlobalValue: globalValue,
			})
		}
	}

	return overrides
}

// ExtractRepoPath derives an "owner/repo" style path from a git remote URL.
func ExtractRepoPath(remoteURL string) string {
	url := strings.TrimSuffix(remoteURL, ".git")

	switch {
	case strings.HasPrefix(url, "git@"):
		url = strings.TrimPrefix(url, "git@")
		url = strings.Replace(url, ":", "/", 1)
	case strings.HasPrefix(url, "https://"):
		url = strings.TrimPrefix(url, "https://")
	case strings.HasPrefix(url, "http://"):
		url = strings.TrimPrefix(url, "http://")
	}

	parts := strings.Split(url, "/")
	if len(parts) >= minRemoteURLPathParts {
		return filepath.Join(parts[len(parts)-2], parts[len(parts)-1])
	}

	return ""
}

// DefaultBranch is a repo's default branch and the commit it points at, read
// from origin/HEAD.
type DefaultBranch struct {
	Name string
	SHA  string
}

// DefaultBranchHead resolves origin/HEAD. It fails for a repo whose remote has
// no HEAD ref, which is the case for one that has never been fetched.
func DefaultBranchHead(ctx context.Context, repoPath string) (DefaultBranch, error) {
	name, err := runCommand(ctx, repoPath, "git", "rev-parse", "--abbrev-ref", "origin/HEAD")
	if err != nil {
		return DefaultBranch{}, fmt.Errorf("resolving origin/HEAD: %w", err)
	}

	sha, err := runCommand(ctx, repoPath, "git", "rev-parse", "origin/HEAD")
	if err != nil {
		return DefaultBranch{}, fmt.Errorf("resolving origin/HEAD commit: %w", err)
	}

	return DefaultBranch{Name: strings.TrimPrefix(name, "origin/"), SHA: sha}, nil
}
