package vcs_test

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kyleking/gh-repo-dashboard/internal/models"
	"github.com/kyleking/gh-repo-dashboard/internal/vcs"
)

const testRepoPath = "/repo"

// assertListResult checks the common shape shared by the git/jj list-parsing
// tests below: an error-state match, then an element-by-element comparison.
func assertListResult[T comparable](t *testing.T, wantErr bool, expected, got []T, err error, label string) {
	t.Helper()

	if (err != nil) != wantErr {
		t.Fatalf("unexpected error state: %v", err)
	}
	if len(got) != len(expected) {
		t.Fatalf("expected %d %s, got %d", len(expected), label, len(got))
	}
	for i, exp := range expected {
		if got[i] != exp {
			t.Errorf("%s %d: expected %+v, got %+v", label, i, exp, got[i])
		}
	}
}

var (
	errUnexpectedCommand = errors.New("unexpected command")
	errBoom              = errors.New("boom")
	errNoUpstream        = errors.New("no upstream")
	errNoSuchRemote      = errors.New("no such remote")
	errNetworkDown       = errors.New("network down")
	errNoRemote          = errors.New("no remote")
	errUnknownRevision   = errors.New("unknown revision")
)

// stubCommands returns a context that makes runCommand answer from canned/failures
// instead of shelling out. It's local to the returned context, so subtests using
// their own stubCommands call can run with t.Parallel() safely.
func stubCommands(t *testing.T, canned map[string]string, failures map[string]error) context.Context {
	t.Helper()

	stub := func(_ context.Context, _, name string, args ...string) (string, error) {
		key := name + " " + strings.Join(args, " ")
		if err, ok := failures[key]; ok {
			return "", err
		}
		if out, ok := canned[key]; ok {
			return out, nil
		}

		return "", fmt.Errorf("%s: %w", key, errUnexpectedCommand)
	}

	return vcs.WithCommandRunner(context.Background(), stub)
}

func TestRunCommandReal(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	t.Run("success trims output", func(t *testing.T) {
		t.Parallel()
		out, err := vcs.RunCommand(context.Background(), t.TempDir(), "git", "--version")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(out, "git version") {
			t.Errorf("unexpected output: %q", out)
		}
		if strings.HasSuffix(out, "\n") {
			t.Error("output not trimmed")
		}
	})

	t.Run("missing binary returns error", func(t *testing.T) {
		t.Parallel()
		if _, err := vcs.RunCommand(context.Background(), t.TempDir(), "definitely-missing-binary-xyz"); err == nil {
			t.Error("expected error")
		}
	})

	t.Run("exit error is wrapped by runGit", func(t *testing.T) {
		t.Parallel()
		g := vcs.NewGitOperations()
		_, err := g.RunGitForTest(context.Background(), t.TempDir(), "rev-parse", "--verify", "HEAD")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.HasPrefix(err.Error(), "git rev-parse --verify HEAD:") {
			t.Errorf("unexpected error message: %v", err)
		}
	})
}

func TestGitRunGitWrapsExitError(t *testing.T) {
	t.Parallel()
	ctx := stubCommands(t, nil, map[string]error{
		"git status --porcelain -z": &exec.ExitError{Stderr: []byte("fatal: not a repo")},
	})

	g := vcs.NewGitOperations()
	_, err := g.RunGitForTest(ctx, testRepoPath, "status", "--porcelain", "-z")
	if err == nil {
		t.Fatal("expected error")
	}
	expected := "git status --porcelain -z: fatal: not a repo: command failed"
	if err.Error() != expected {
		t.Errorf("expected %q, got %q", expected, err.Error())
	}
	if !errors.Is(err, vcs.ErrCommandFailed) {
		t.Error("expected error to wrap vcs.ErrCommandFailed")
	}
}

func TestGitGetCurrentBranch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		canned   map[string]string
		failures map[string]error
		expected string
		wantErr  bool
	}{
		{
			name:     "on branch",
			canned:   map[string]string{"git rev-parse --abbrev-ref HEAD": "main"},
			expected: "main",
		},
		{
			name: "detached head uses short hash",
			canned: map[string]string{
				"git rev-parse --abbrev-ref HEAD": "HEAD",
				"git rev-parse --short HEAD":      "abc1234",
			},
			expected: "(abc1234)",
		},
		{
			name:     "detached head with short hash failure",
			canned:   map[string]string{"git rev-parse --abbrev-ref HEAD": "HEAD"},
			failures: map[string]error{"git rev-parse --short HEAD": errBoom},
			expected: "HEAD",
		},
		{
			name:     "command failure",
			failures: map[string]error{"git rev-parse --abbrev-ref HEAD": errBoom},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := stubCommands(t, tt.canned, tt.failures)

			g := vcs.NewGitOperations()
			branch, err := g.GetCurrentBranch(ctx, testRepoPath)
			if (err != nil) != tt.wantErr {
				t.Fatalf("unexpected error state: %v", err)
			}
			if branch != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, branch)
			}
		})
	}
}

func TestGitGetUpstream(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		canned   map[string]string
		failures map[string]error
		expected string
		wantErr  bool
	}{
		{
			name:     "has upstream",
			canned:   map[string]string{"git rev-parse --abbrev-ref main@{upstream}": "origin/main"},
			expected: "origin/main",
		},
		{
			name:     "no upstream configured",
			failures: map[string]error{"git rev-parse --abbrev-ref main@{upstream}": errNoUpstream},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := stubCommands(t, tt.canned, tt.failures)

			g := vcs.NewGitOperations()
			upstream, err := g.GetUpstream(ctx, testRepoPath, "main")
			if (err != nil) != tt.wantErr {
				t.Fatalf("unexpected error state: %v", err)
			}
			if upstream != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, upstream)
			}
		})
	}
}

func TestGitGetAheadBehind(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		canned   map[string]string
		failures map[string]error
		ahead    int
		behind   int
		wantErr  bool
	}{
		{
			name:   "ahead and behind",
			canned: map[string]string{"git rev-list --left-right --count main...origin/main": "3\t2"},
			ahead:  3,
			behind: 2,
		},
		{
			name:    "malformed output",
			canned:  map[string]string{"git rev-list --left-right --count main...origin/main": "garbage"},
			wantErr: true,
		},
		{
			name:     "command failure",
			failures: map[string]error{"git rev-list --left-right --count main...origin/main": errBoom},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := stubCommands(t, tt.canned, tt.failures)

			g := vcs.NewGitOperations()
			ahead, behind, err := g.GetAheadBehind(ctx, testRepoPath, "main", "origin/main")
			if (err != nil) != tt.wantErr {
				t.Fatalf("unexpected error state: %v", err)
			}
			if ahead != tt.ahead || behind != tt.behind {
				t.Errorf("expected %d/%d, got %d/%d", tt.ahead, tt.behind, ahead, behind)
			}
		})
	}
}

// compareBranchesTest is shared by the git and jj CompareBranches exec tests.
type compareBranchesTest struct {
	name     string
	canned   map[string]string
	failures map[string]error
	ahead    int
	behind   int
	wantErr  bool
}

func runCompareBranchesTests(t *testing.T, ops vcs.Operations, tests []compareBranchesTest) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := stubCommands(t, tt.canned, tt.failures)

			ahead, behind, err := ops.CompareBranches(ctx, testRepoPath, "feature/login", "main")
			if (err != nil) != tt.wantErr {
				t.Fatalf("unexpected error state: %v", err)
			}
			if ahead != tt.ahead || behind != tt.behind {
				t.Errorf("expected %d/%d, got %d/%d", tt.ahead, tt.behind, ahead, behind)
			}
		})
	}
}

func TestGitCompareBranches(t *testing.T) {
	t.Parallel()
	runCompareBranchesTests(t, vcs.NewGitOperations(), []compareBranchesTest{
		{
			name:   "ahead and behind",
			canned: map[string]string{"git rev-list --left-right --count feature/login...main": "2\t1"},
			ahead:  2,
			behind: 1,
		},
		{
			name:   "up to date",
			canned: map[string]string{"git rev-list --left-right --count feature/login...main": "0\t0"},
		},
		{
			name:     "command failure",
			failures: map[string]error{"git rev-list --left-right --count feature/login...main": errBoom},
			wantErr:  true,
		},
	})
}

func TestGitStatusCountMethods(t *testing.T) {
	t.Parallel()
	ctx := stubCommands(t, map[string]string{
		"git status --porcelain -z": "M  staged.txt\x00 M unstaged.txt\x00?? new.txt\x00UU conflict.txt\x00",
	}, nil)

	g := vcs.NewGitOperations()

	tests := []struct {
		name     string
		fn       func(context.Context, string) (int, error)
		expected int
	}{
		{"staged", g.GetStagedCount, 1},
		{"unstaged", g.GetUnstagedCount, 1},
		{"untracked", g.GetUntrackedCount, 1},
		{"conflicted", g.GetConflictedCount, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			count, err := tt.fn(ctx, testRepoPath)
			if err != nil {
				t.Fatal(err)
			}
			if count != tt.expected {
				t.Errorf("expected %d, got %d", tt.expected, count)
			}
		})
	}
}

func TestGitStatusCounts_PorcelainEdgeCases(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                                    string
		porcelain                               string
		staged, unstaged, untracked, conflicted int
	}{
		{
			name:      "leading unstaged entry keeps its status space",
			porcelain: " M a.txt\x00M  b.txt\x00?? c.txt\x00",
			staged:    1,
			unstaged:  1,
			untracked: 1,
		},
		{
			name:      "rename origin path is not a second entry",
			porcelain: "R  new.go\x00old.go\x00",
			staged:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := stubCommands(t, map[string]string{"git status --porcelain -z": tt.porcelain}, nil)

			g := vcs.NewGitOperations()
			counts := []struct {
				name     string
				fn       func(context.Context, string) (int, error)
				expected int
			}{
				{"staged", g.GetStagedCount, tt.staged},
				{"unstaged", g.GetUnstagedCount, tt.unstaged},
				{"untracked", g.GetUntrackedCount, tt.untracked},
				{"conflicted", g.GetConflictedCount, tt.conflicted},
			}

			for _, c := range counts {
				got, err := c.fn(ctx, testRepoPath)
				if err != nil {
					t.Fatal(err)
				}
				if got != c.expected {
					t.Errorf("%s: expected %d, got %d", c.name, c.expected, got)
				}
			}
		})
	}
}

type gitRepoSummaryCase struct {
	name     string
	canned   map[string]string
	failures map[string]error
	expected models.RepoSummary
	wantErr  bool
}

func gitRepoSummaryCases() []gitRepoSummaryCase {
	return []gitRepoSummaryCase{
		{
			name: "clean repo with upstream",
			canned: map[string]string{
				"git rev-parse --abbrev-ref HEAD":                      "main",
				"git rev-parse --abbrev-ref main@{upstream}":           "origin/main",
				"git rev-list --left-right --count main...origin/main": "0\t0",
				"git status --porcelain -z":                            "",
				"git stash list":                                       "",
				"git log -1 --format=%ct":                              "1700000000",
			},
			expected: models.RepoSummary{
				Path:         testRepoPath,
				VCSType:      models.VCSTypeGit,
				Branch:       "main",
				Upstream:     "origin/main",
				LastModified: time.Unix(1700000000, 0),
			},
		},
		{
			name: "dirty repo ahead and behind",
			canned: map[string]string{
				"git rev-parse --abbrev-ref HEAD":                      "main",
				"git rev-parse --abbrev-ref main@{upstream}":           "origin/main",
				"git rev-list --left-right --count main...origin/main": "3\t2",
				"git status --porcelain -z":                            "M  a.txt\x00 M b.txt\x00?? c.txt\x00",
				"git stash list":                                       "stash@{0}: WIP on main\nstash@{1}: save",
				"git log -1 --format=%ct":                              "1700000000",
			},
			expected: models.RepoSummary{
				Path:         testRepoPath,
				VCSType:      models.VCSTypeGit,
				Branch:       "main",
				Upstream:     "origin/main",
				Ahead:        3,
				Behind:       2,
				Staged:       1,
				Unstaged:     1,
				Untracked:    1,
				StashCount:   2,
				LastModified: time.Unix(1700000000, 0),
			},
		},
		{
			name: "remote url populates protocol and repo",
			canned: map[string]string{
				"git rev-parse --abbrev-ref HEAD":                      "main",
				"git rev-parse --abbrev-ref main@{upstream}":           "origin/main",
				"git rev-list --left-right --count main...origin/main": "0\t0",
				"git status --porcelain -z":                            "",
				"git stash list":                                       "",
				"git log -1 --format=%ct":                              "1700000000",
				"git remote get-url origin":                            "git@github.com:acme/app.git",
			},
			expected: models.RepoSummary{
				Path:           testRepoPath,
				VCSType:        models.VCSTypeGit,
				Branch:         "main",
				Upstream:       "origin/main",
				LastModified:   time.Unix(1700000000, 0),
				RemoteProtocol: "ssh",
				RemoteRepo:     "acme/app",
			},
		},
		{
			name: "no upstream skips ahead behind",
			canned: map[string]string{
				"git rev-parse --abbrev-ref HEAD": "feature",
				"git status --porcelain -z":       "",
				"git stash list":                  "",
				"git log -1 --format=%ct":         "1700000000",
			},
			failures: map[string]error{
				"git rev-parse --abbrev-ref feature@{upstream}": errNoUpstream,
			},
			expected: models.RepoSummary{
				Path:         testRepoPath,
				VCSType:      models.VCSTypeGit,
				Branch:       "feature",
				LastModified: time.Unix(1700000000, 0),
			},
		},
		{
			name: "branch failure returns error",
			failures: map[string]error{
				"git rev-parse --abbrev-ref HEAD": errBoom,
			},
			wantErr: true,
		},
	}
}

func TestGitGetRepoSummary(t *testing.T) {
	t.Parallel()

	for _, tt := range gitRepoSummaryCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := stubCommands(t, tt.canned, tt.failures)

			g := vcs.NewGitOperations()
			summary, err := g.GetRepoSummary(ctx, testRepoPath)
			if (err != nil) != tt.wantErr {
				t.Fatalf("unexpected error state: %v", err)
			}
			if tt.wantErr {
				return
			}
			//nolint:govet // Error is always nil here; comparing NotesFiles/pointers, not errors
			if !reflect.DeepEqual(summary, tt.expected) {
				t.Errorf("expected %+v, got %+v", tt.expected, summary)
			}
		})
	}
}

func TestGitGetBranchList(t *testing.T) {
	t.Parallel()
	key := "git for-each-ref --format=%(refname:short)\t%(upstream:short)\t" +
		"%(upstream:track)\t%(committerdate:unix)\t%(HEAD)\t%(objectname) refs/heads/"

	tests := []struct {
		name     string
		canned   map[string]string
		failures map[string]error
		expected []models.BranchInfo
		wantErr  bool
	}{
		{
			name: "mixed branches",
			canned: map[string]string{
				key: "dev\t\t\t1680000000\t \tabc111\n" +
					"feature\torigin/feature\t[behind 3]\t1690000000\t \tbcd222\n" +
					"main\torigin/main\t[ahead 2, behind 1]\t1700000000\t*\tcde333",
			},
			expected: []models.BranchInfo{
				{Name: "dev", LastCommit: time.Unix(1680000000, 0), Head: "abc111"},
				{
					Name: "feature", Upstream: "origin/feature", Behind: 3,
					LastCommit: time.Unix(1690000000, 0), Head: "bcd222",
				},
				{
					Name: "main", Upstream: "origin/main", Ahead: 2, Behind: 1,
					LastCommit: time.Unix(1700000000, 0), IsCurrent: true, Head: "cde333",
				},
			},
		},
		{
			name: "last branch without upstream survives output trimming",
			canned: map[string]string{
				key: "main\torigin/main\t\t1700000000\t*\tcde333\n" +
					"zz-experiment\t\t\t1680000000",
			},
			expected: []models.BranchInfo{
				{
					Name: "main", Upstream: "origin/main",
					LastCommit: time.Unix(1700000000, 0), IsCurrent: true, Head: "cde333",
				},
				{Name: "zz-experiment", LastCommit: time.Unix(1680000000, 0)},
			},
		},
		{
			name:     "empty output",
			canned:   map[string]string{key: ""},
			expected: nil,
		},
		{
			name:     "command failure",
			failures: map[string]error{key: errBoom},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := stubCommands(t, tt.canned, tt.failures)

			g := vcs.NewGitOperations()
			branches, err := g.GetBranchList(ctx, testRepoPath)
			assertListResult(t, tt.wantErr, tt.expected, branches, err, "branch")
		})
	}
}

func TestGitGetStashList(t *testing.T) {
	t.Parallel()
	key := "git stash list --format=%gd\t%gs\t%ct"

	tests := []struct {
		name     string
		canned   map[string]string
		failures map[string]error
		expected []models.StashDetail
		wantErr  bool
	}{
		{
			name: "two stashes",
			canned: map[string]string{
				key: "stash@{0}\tWIP on main: abc msg\t1700000000\n" +
					"stash@{1}\tsaved work\t1690000000",
			},
			expected: []models.StashDetail{
				{Index: 0, Message: "WIP on main: abc msg", Date: time.Unix(1700000000, 0)},
				{Index: 1, Message: "saved work", Date: time.Unix(1690000000, 0)},
			},
		},
		{
			name:     "no stashes",
			canned:   map[string]string{key: ""},
			expected: nil,
		},
		{
			name:     "command failure",
			failures: map[string]error{key: errBoom},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := stubCommands(t, tt.canned, tt.failures)

			g := vcs.NewGitOperations()
			stashes, err := g.GetStashList(ctx, testRepoPath)
			assertListResult(t, tt.wantErr, tt.expected, stashes, err, "stash")
		})
	}
}

func TestGitGetWorktreeList(t *testing.T) {
	t.Parallel()
	key := "git worktree list --porcelain"

	tests := []struct {
		name     string
		canned   map[string]string
		failures map[string]error
		expected []models.WorktreeInfo
		wantErr  bool
	}{
		{
			name: "main and locked feature worktrees",
			canned: map[string]string{
				key: "worktree /repo\nHEAD abc123\nbranch refs/heads/main\n\n" +
					"worktree /repo-feature\nHEAD def456\nbranch refs/heads/feature\nlocked",
			},
			expected: []models.WorktreeInfo{
				{Path: "/repo", Branch: "main"},
				{Path: "/repo-feature", Branch: "feature", IsLocked: true},
			},
		},
		{
			name:   "bare worktree",
			canned: map[string]string{key: "worktree /repo.git\nbare"},
			expected: []models.WorktreeInfo{
				{Path: "/repo.git", IsBare: true},
			},
		},
		{
			name:     "command failure",
			failures: map[string]error{key: errBoom},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := stubCommands(t, tt.canned, tt.failures)

			g := vcs.NewGitOperations()
			worktrees, err := g.GetWorktreeList(ctx, testRepoPath)
			assertListResult(t, tt.wantErr, tt.expected, worktrees, err, "worktree")
		})
	}
}

func TestGitGetCommitLog(t *testing.T) {
	t.Parallel()
	key := "git log -n2 --format=%H\t%h\t%s\t%an\t%ct"

	//nolint:dupl // same table shape as TestJJGetCommitLog, different VCS output formats/literals
	tests := []struct {
		name     string
		canned   map[string]string
		failures map[string]error
		expected []models.CommitInfo
		wantErr  bool
	}{
		{
			name: "two commits",
			canned: map[string]string{
				key: "aaaa1111\taaaa\tfeat: add thing\tKyle King\t1700000000\n" +
					"bbbb2222\tbbbb\tfix: bug\tOther Dev\t1690000000",
			},
			expected: []models.CommitInfo{
				{
					Hash: "aaaa1111", ShortHash: "aaaa", Subject: "feat: add thing",
					Author: "Kyle King", Date: time.Unix(1700000000, 0),
				},
				{
					Hash: "bbbb2222", ShortHash: "bbbb", Subject: "fix: bug",
					Author: "Other Dev", Date: time.Unix(1690000000, 0),
				},
			},
		},
		{
			name:     "malformed lines skipped",
			canned:   map[string]string{key: "not-enough-fields"},
			expected: nil,
		},
		{
			name:     "command failure",
			failures: map[string]error{key: errBoom},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := stubCommands(t, tt.canned, tt.failures)

			g := vcs.NewGitOperations()
			commits, err := g.GetCommitLog(ctx, testRepoPath, 2)
			assertListResult(t, tt.wantErr, tt.expected, commits, err, "commit")
		})
	}
}

func TestGitGetLastModified(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		canned   map[string]string
		failures map[string]error
		expected int64
		wantErr  bool
	}{
		{
			name:     "valid timestamp",
			canned:   map[string]string{"git log -1 --format=%ct": "1700000000"},
			expected: 1700000000,
		},
		{
			name:     "command failure",
			failures: map[string]error{"git log -1 --format=%ct": errBoom},
			wantErr:  true,
		},
		{
			name:    "non-numeric output",
			canned:  map[string]string{"git log -1 --format=%ct": "garbage"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := stubCommands(t, tt.canned, tt.failures)

			g := vcs.NewGitOperations()
			ts, err := g.GetLastModified(ctx, testRepoPath)
			if (err != nil) != tt.wantErr {
				t.Fatalf("unexpected error state: %v", err)
			}
			if ts != tt.expected {
				t.Errorf("expected %d, got %d", tt.expected, ts)
			}
		})
	}
}

func TestGitGetRemoteURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		canned   map[string]string
		failures map[string]error
		expected string
		wantErr  bool
	}{
		{
			name:     "has origin",
			canned:   map[string]string{"git remote get-url origin": "git@github.com:owner/repo.git"},
			expected: "git@github.com:owner/repo.git",
		},
		{
			name:     "no origin",
			failures: map[string]error{"git remote get-url origin": errNoSuchRemote},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := stubCommands(t, tt.canned, tt.failures)

			g := vcs.NewGitOperations()
			url, err := g.GetRemoteURL(ctx, testRepoPath)
			if (err != nil) != tt.wantErr {
				t.Fatalf("unexpected error state: %v", err)
			}
			if url != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, url)
			}
		})
	}
}

func TestGitFetchAllAndPruneRemote(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		canned     map[string]string
		failures   map[string]error
		run        func(context.Context, *vcs.GitOperations) (bool, string, error)
		expectedOK bool
		expected   string
	}{
		{
			name:   "fetch success",
			canned: map[string]string{"git fetch --all --prune": ""},
			run: func(ctx context.Context, g *vcs.GitOperations) (bool, string, error) {
				return g.FetchAll(ctx, testRepoPath)
			},
			expectedOK: true,
			expected:   "Fetched from all remotes",
		},
		{
			name:     "fetch failure",
			failures: map[string]error{"git fetch --all --prune": errNetworkDown},
			run: func(ctx context.Context, g *vcs.GitOperations) (bool, string, error) {
				return g.FetchAll(ctx, testRepoPath)
			},
			expectedOK: false,
			expected:   "network down",
		},
		{
			name:   "prune success",
			canned: map[string]string{"git remote prune origin": ""},
			run: func(ctx context.Context, g *vcs.GitOperations) (bool, string, error) {
				return g.PruneRemote(ctx, testRepoPath)
			},
			expectedOK: true,
			expected:   "Pruned stale remote branches",
		},
		{
			name:     "prune failure",
			failures: map[string]error{"git remote prune origin": errNoRemote},
			run: func(ctx context.Context, g *vcs.GitOperations) (bool, string, error) {
				return g.PruneRemote(ctx, testRepoPath)
			},
			expectedOK: false,
			expected:   "no remote",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := stubCommands(t, tt.canned, tt.failures)

			ok, msg, err := tt.run(ctx, vcs.NewGitOperations())
			if err != nil {
				t.Fatal(err)
			}
			if ok != tt.expectedOK {
				t.Errorf("expected ok=%v, got %v", tt.expectedOK, ok)
			}
			if msg != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, msg)
			}
		})
	}
}

func TestGitPushAndSwitchBranch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		canned     map[string]string
		failures   map[string]error
		run        func(context.Context, *vcs.GitOperations) (bool, string, error)
		expectedOK bool
		expected   string
	}{
		{
			name:   "push follows tags",
			canned: map[string]string{"git push --follow-tags origin feature": ""},
			run: func(ctx context.Context, g *vcs.GitOperations) (bool, string, error) {
				return g.PushBranch(ctx, testRepoPath, "feature", false)
			},
			expectedOK: true,
			expected:   "Pushed feature to origin",
		},
		{
			name:   "push sets upstream when asked",
			canned: map[string]string{"git push --follow-tags --set-upstream origin feature": ""},
			run: func(ctx context.Context, g *vcs.GitOperations) (bool, string, error) {
				return g.PushBranch(ctx, testRepoPath, "feature", true)
			},
			expectedOK: true,
			expected:   "Pushed feature to origin",
		},
		{
			name:     "push failure reports the message",
			failures: map[string]error{"git push --follow-tags origin feature": errNetworkDown},
			run: func(ctx context.Context, g *vcs.GitOperations) (bool, string, error) {
				return g.PushBranch(ctx, testRepoPath, "feature", false)
			},
			expectedOK: false,
			expected:   "network down",
		},
		{
			name:   "switch success",
			canned: map[string]string{"git switch feature": ""},
			run: func(ctx context.Context, g *vcs.GitOperations) (bool, string, error) {
				return g.SwitchBranch(ctx, testRepoPath, "feature")
			},
			expectedOK: true,
			expected:   "Switched to feature",
		},
		{
			name:     "switch failure reports the message",
			failures: map[string]error{"git switch feature": errNoRemote},
			run: func(ctx context.Context, g *vcs.GitOperations) (bool, string, error) {
				return g.SwitchBranch(ctx, testRepoPath, "feature")
			},
			expectedOK: false,
			expected:   "no remote",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := stubCommands(t, tt.canned, tt.failures)

			ok, msg, err := tt.run(ctx, vcs.NewGitOperations())
			if err != nil {
				t.Fatal(err)
			}
			if ok != tt.expectedOK {
				t.Errorf("expected ok=%v, got %v", tt.expectedOK, ok)
			}
			if msg != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, msg)
			}
		})
	}
}

const symbolicRefKey = "git symbolic-ref refs/remotes/origin/HEAD"

// mergedRefsKey is the command key CleanupMergedBranches uses to list the
// branches merged into branch.
func mergedRefsKey(branch string) string {
	return "git for-each-ref --format=%(refname:short) --merged " + branch + " refs/heads"
}

type cleanupCase struct {
	name         string
	canned       map[string]string
	failures     map[string]error
	squashMerged []string
	expectedOK   bool
	expected     string
}

//nolint:dupl // same table shape as TestJJCleanupMergedBranches, different VCS output formats/literals
func runCleanupCases(t *testing.T, tests []cleanupCase) {
	t.Helper()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := stubCommands(t, tt.canned, tt.failures)

			g := vcs.NewGitOperations()
			ok, msg, err := g.CleanupMergedBranches(ctx, testRepoPath, tt.squashMerged)
			if err != nil {
				t.Fatal(err)
			}
			if ok != tt.expectedOK {
				t.Errorf("expected ok=%v, got %v", tt.expectedOK, ok)
			}
			if msg != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, msg)
			}
		})
	}
}

func TestGitCleanupMergedBranches(t *testing.T) {
	t.Parallel()
	runCleanupCases(t, []cleanupCase{
		{
			name: "deletes merged branches via origin/HEAD",
			canned: map[string]string{
				symbolicRefKey:          "refs/remotes/origin/main",
				mergedRefsKey("main"):   "feature\nmain\nold-fix",
				"git branch -d feature": "",
				"git branch -d old-fix": "",
			},
			expectedOK: true,
			expected:   "Deleted 2 branches: feature, old-fix",
		},
		{
			name: "falls back to main when origin/HEAD is unset",
			canned: map[string]string{
				"git rev-parse --verify main": "abc123",
				mergedRefsKey("main"):         "feature\nmain",
				"git branch -d feature":       "",
			},
			failures: map[string]error{
				symbolicRefKey: errNoSuchRemote,
			},
			expectedOK: true,
			expected:   "Deleted 1 branches: feature",
		},
		{
			name: "protects local trunk branch alongside the resolved default",
			canned: map[string]string{
				symbolicRefKey:          "refs/remotes/origin/main",
				mergedRefsKey("main"):   "feature\nmain\ntrunk",
				"git branch -d feature": "",
			},
			expectedOK: true,
			expected:   "Deleted 1 branches: feature",
		},
		{
			name: "falls back to master",
			canned: map[string]string{
				"git rev-parse --verify master": "abc123",
				mergedRefsKey("master"):         "master",
			},
			failures: map[string]error{
				symbolicRefKey:                errNoSuchRemote,
				"git rev-parse --verify main": errUnknownRevision,
			},
			expectedOK: true,
			expected:   "No merged branches to delete",
		},
		{
			name: "neither main nor master",
			failures: map[string]error{
				symbolicRefKey:                  errNoSuchRemote,
				"git rev-parse --verify main":   errUnknownRevision,
				"git rev-parse --verify master": errUnknownRevision,
			},
			expectedOK: false,
			expected:   "Could not find main or master branch",
		},
		{
			name: "merged listing failure",
			canned: map[string]string{
				symbolicRefKey: "refs/remotes/origin/main",
			},
			failures: map[string]error{
				mergedRefsKey("main"): errBoom,
			},
			expectedOK: false,
			expected:   "boom",
		},
	})
}

func TestGitCleanupMergedBranchesGuards(t *testing.T) {
	t.Parallel()

	const worktreeListKey = "git worktree list --porcelain"

	runCleanupCases(t, []cleanupCase{
		{
			name: "skips branches checked out here or in a worktree",
			canned: map[string]string{
				symbolicRefKey:                    "refs/remotes/origin/main",
				mergedRefsKey("main"):             "feature\nheld\nwt-branch",
				"git rev-parse --abbrev-ref HEAD": "held",
				worktreeListKey:                   "worktree /tmp/wt\nHEAD abc\nbranch refs/heads/wt-branch\n",
				"git branch -d feature":           "",
			},
			expectedOK: true,
			expected:   "Deleted 1 branches: feature",
		},
		{
			name: "reports failure when a deletion fails",
			canned: map[string]string{
				symbolicRefKey:        "refs/remotes/origin/main",
				mergedRefsKey("main"): "feature",
			},
			failures: map[string]error{
				"git branch -d feature": errBoom,
			},
			expectedOK: false,
			expected:   "Failed to delete 1 branches: feature (boom)",
		},
	})
}

// forEachRefKey is the git for-each-ref command key used by GetBranchList,
// which CleanupMergedBranches' squash-merged path calls via localBranchNames.
const forEachRefKey = "git for-each-ref --format=%(refname:short)\t%(upstream:short)\t" +
	"%(upstream:track)\t%(committerdate:unix)\t%(HEAD)\t%(objectname) refs/heads/"

func TestGitCleanupMergedBranchesSquashMerged(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		canned       map[string]string
		squashMerged []string
		expected     string
	}{
		{
			name: "deletes squash-merged branches with -D",
			canned: map[string]string{
				symbolicRefKey:                    "refs/remotes/origin/main",
				mergedRefsKey("main"):             "main",
				"git rev-parse --abbrev-ref HEAD": "main",
				"git worktree list --porcelain":   "",
				forEachRefKey:                     "squashed\t\t\t1700000000\t\tabc\nmain\t\t\t1700000000\t*\tdef",
				"git branch -D squashed":          "",
			},
			squashMerged: []string{"squashed"},
			expected:     "Deleted 1 branches: squashed",
		},
		{
			name: "skips squash-merged branch that's the current branch",
			canned: map[string]string{
				symbolicRefKey:                    "refs/remotes/origin/main",
				mergedRefsKey("main"):             "main",
				"git rev-parse --abbrev-ref HEAD": "squashed",
				"git worktree list --porcelain":   "",
				forEachRefKey:                     "squashed\t\t\t1700000000\t*\tabc",
			},
			squashMerged: []string{"squashed"},
			expected:     "No merged branches to delete",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := stubCommands(t, tt.canned, nil)

			g := vcs.NewGitOperations()
			ok, msg, err := g.CleanupMergedBranches(ctx, testRepoPath, tt.squashMerged)
			if err != nil {
				t.Fatal(err)
			}
			if !ok {
				t.Error("expected ok=true")
			}
			if msg != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, msg)
			}
		})
	}
}
