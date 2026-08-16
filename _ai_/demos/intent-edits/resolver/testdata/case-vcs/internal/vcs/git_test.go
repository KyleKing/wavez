package vcs_test

import (
	"bufio"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/kyleking/gh-repo-dashboard/internal/vcs"
)

func TestExtractRepoPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "ssh url",
			input:    "git@github.com:owner/repo.git",
			expected: "owner/repo",
		},
		{
			name:     "ssh url without .git",
			input:    "git@github.com:owner/repo",
			expected: "owner/repo",
		},
		{
			name:     "https url",
			input:    "https://github.com/owner/repo.git",
			expected: "owner/repo",
		},
		{
			name:     "https url without .git",
			input:    "https://github.com/owner/repo",
			expected: "owner/repo",
		},
		{
			name:     "http url",
			input:    "http://github.com/owner/repo.git",
			expected: "owner/repo",
		},
		{
			name:     "gitlab ssh",
			input:    "git@gitlab.com:group/subgroup/repo.git",
			expected: "subgroup/repo",
		},
		{
			name:     "short path returns empty",
			input:    "invalid",
			expected: "",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := vcs.ExtractRepoPath(tt.input)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func parseStatusCountsTestCases() []struct {
	name       string
	input      string
	staged     int
	unstaged   int
	untracked  int
	conflicted int
} {
	return []struct {
		name       string
		input      string
		staged     int
		unstaged   int
		untracked  int
		conflicted int
	}{
		{
			name:       "empty status",
			input:      "",
			staged:     0,
			unstaged:   0,
			untracked:  0,
			conflicted: 0,
		},
		{
			name:      "staged file",
			input:     "M  file.txt\x00",
			staged:    1,
			unstaged:  0,
			untracked: 0,
		},
		{
			name:      "unstaged file",
			input:     " M file.txt\x00",
			staged:    0,
			unstaged:  1,
			untracked: 0,
		},
		{
			name:      "untracked file",
			input:     "?? file.txt\x00",
			staged:    0,
			unstaged:  0,
			untracked: 1,
		},
		{
			name:       "conflicted UU",
			input:      "UU file.txt\x00",
			conflicted: 1,
		},
		{
			name:       "conflicted DD",
			input:      "DD file.txt\x00",
			conflicted: 1,
		},
		{
			name:       "conflicted AA",
			input:      "AA file.txt\x00",
			conflicted: 1,
		},
		{
			name:      "mixed status",
			input:     "M  staged.txt\x00 M unstaged.txt\x00?? new.txt\x00",
			staged:    1,
			unstaged:  1,
			untracked: 1,
		},
		{
			name:     "added file",
			input:    "A  new.txt\x00",
			staged:   1,
			unstaged: 0,
		},
		{
			name:     "deleted file staged",
			input:    "D  old.txt\x00",
			staged:   1,
			unstaged: 0,
		},
		{
			name:     "renamed file",
			input:    "R  old.txt -> new.txt\x00",
			staged:   1,
			unstaged: 0,
		},
		{
			name:     "modified both staged and unstaged",
			input:    "MM file.txt\x00",
			staged:   1,
			unstaged: 1,
		},
	}
}

func TestParseStatusCounts(t *testing.T) {
	t.Parallel()

	for _, tt := range parseStatusCountsTestCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			staged, unstaged, untracked, conflicted := parseStatusOutput(tt.input)
			if staged != tt.staged {
				t.Errorf("staged: expected %d, got %d", tt.staged, staged)
			}
			if unstaged != tt.unstaged {
				t.Errorf("unstaged: expected %d, got %d", tt.unstaged, unstaged)
			}
			if untracked != tt.untracked {
				t.Errorf("untracked: expected %d, got %d", tt.untracked, untracked)
			}
			if conflicted != tt.conflicted {
				t.Errorf("conflicted: expected %d, got %d", tt.conflicted, conflicted)
			}
		})
	}
}

// classifyStatusEntry mirrors GitOperations.classifyPorcelainEntry for testing
// the porcelain XY status code classification without shelling out to git.
//
//nolint:gocritic // named results trip nonamedreturns instead; (staged, unstaged, untracked, conflicted) by position
func classifyStatusEntry(x, y byte) (int, int, int, int) {
	switch {
	case x == 'U' || y == 'U' || (x == 'D' && y == 'D') || (x == 'A' && y == 'A'):
		return 0, 0, 0, 1
	case x == '?':
		return 0, 0, 1, 0
	default:
		var staged, unstaged int
		if x != ' ' && x != '?' {
			staged = 1
		}
		if y != ' ' && y != '?' {
			unstaged = 1
		}

		return staged, unstaged, 0, 0
	}
}

//nolint:gocritic // named results trip nonamedreturns instead; (staged, unstaged, untracked, conflicted) by position
func parseStatusOutput(out string) (int, int, int, int) {
	var staged, unstaged, untracked, conflicted int

	entries := strings.Split(out, "\x00")
	for _, entry := range entries {
		if len(entry) < 2 {
			continue
		}

		s, u, ut, c := classifyStatusEntry(entry[0], entry[1])
		staged += s
		unstaged += u
		untracked += ut
		conflicted += c
	}

	return staged, unstaged, untracked, conflicted
}

func TestParseBranchTrackingInfo(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		input  string
		ahead  int
		behind int
	}{
		{
			name:   "ahead only",
			input:  "[ahead 3]",
			ahead:  3,
			behind: 0,
		},
		{
			name:   "behind only",
			input:  "[behind 5]",
			ahead:  0,
			behind: 5,
		},
		{
			name:   "ahead and behind",
			input:  "[ahead 2, behind 4]",
			ahead:  2,
			behind: 4,
		},
		{
			name:   "no tracking info",
			input:  "",
			ahead:  0,
			behind: 0,
		},
		{
			name:   "gone tracking",
			input:  "[gone]",
			ahead:  0,
			behind: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ahead, behind := parseBranchTracking(tt.input)
			if ahead != tt.ahead {
				t.Errorf("ahead: expected %d, got %d", tt.ahead, ahead)
			}
			if behind != tt.behind {
				t.Errorf("behind: expected %d, got %d", tt.behind, behind)
			}
		})
	}
}

// parseBranchTracking returns (ahead, behind) parsed from a git branch tracking annotation.
//
//nolint:gocritic // see doc comment for result order
func parseBranchTracking(s string) (int, int) {
	if s == "" || s == "[gone]" {
		return 0, 0
	}

	trackRe := regexp.MustCompile(`\[ahead (\d+)(?:, behind (\d+))?\]|\[behind (\d+)\]`)
	matches := trackRe.FindStringSubmatch(s)
	if matches == nil {
		return 0, 0
	}

	var ahead, behind int
	if matches[1] != "" {
		ahead, _ = strconv.Atoi(matches[1]) //nolint:errcheck // regex guarantees digits
	}
	if matches[2] != "" {
		behind, _ = strconv.Atoi(matches[2]) //nolint:errcheck // regex guarantees digits
	}
	if matches[3] != "" {
		behind, _ = strconv.Atoi(matches[3]) //nolint:errcheck // regex guarantees digits
	}

	return ahead, behind
}

func TestParseWorktreePorcelain(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected []worktreeResult
	}{
		{
			name:     "empty output",
			input:    "",
			expected: nil,
		},
		{
			name: "single worktree",
			input: `worktree /path/to/repo
branch refs/heads/main
`,
			expected: []worktreeResult{
				{Path: "/path/to/repo", Branch: "main"},
			},
		},
		{
			name: "bare worktree",
			input: `worktree /path/to/repo.git
bare
`,
			expected: []worktreeResult{
				{Path: "/path/to/repo.git", IsBare: true},
			},
		},
		{
			name: "locked worktree",
			input: `worktree /path/to/repo
branch refs/heads/feature
locked
`,
			expected: []worktreeResult{
				{Path: "/path/to/repo", Branch: "feature", IsLocked: true},
			},
		},
		{
			name: "multiple worktrees",
			input: `worktree /main
branch refs/heads/main

worktree /feature
branch refs/heads/feature
`,
			expected: []worktreeResult{
				{Path: "/main", Branch: "main"},
				{Path: "/feature", Branch: "feature"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := parseWorktreePorcelain(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("expected %d worktrees, got %d", len(tt.expected), len(result))
				return
			}
			for i, expected := range tt.expected {
				if result[i] != expected {
					t.Errorf("worktree %d: expected %+v, got %+v", i, expected, result[i])
				}
			}
		})
	}
}

type worktreeResult struct {
	Path     string
	Branch   string
	IsBare   bool
	IsLocked bool
}

func parseWorktreePorcelain(out string) []worktreeResult {
	if out == "" {
		return nil
	}

	var worktrees []worktreeResult
	var current worktreeResult

	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "worktree "):
			if current.Path != "" {
				worktrees = append(worktrees, current)
			}
			current = worktreeResult{Path: strings.TrimPrefix(line, "worktree ")}
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

	return worktrees
}

func TestParseStashList(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{
			name:     "empty",
			input:    "",
			expected: 0,
		},
		{
			name:     "one stash",
			input:    "stash@{0}\tWIP on main: abc123\t1234567890",
			expected: 1,
		},
		{
			name:     "multiple stashes",
			input:    "stash@{0}\tWIP\t123\nstash@{1}\tSave\t456\nstash@{2}\tTest\t789",
			expected: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := countStashEntries(tt.input)
			if result != tt.expected {
				t.Errorf("expected %d, got %d", tt.expected, result)
			}
		})
	}
}

func countStashEntries(out string) int {
	if out == "" {
		return 0
	}

	return len(strings.Split(out, "\n"))
}

func TestParseRevListOutput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		input  string
		ahead  int
		behind int
	}{
		{
			name:   "normal output",
			input:  "3\t2",
			ahead:  3,
			behind: 2,
		},
		{
			name:   "ahead only",
			input:  "5\t0",
			ahead:  5,
			behind: 0,
		},
		{
			name:   "behind only",
			input:  "0\t7",
			ahead:  0,
			behind: 7,
		},
		{
			name:   "no difference",
			input:  "0\t0",
			ahead:  0,
			behind: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ahead, behind := parseRevListOutput(tt.input)
			if ahead != tt.ahead {
				t.Errorf("ahead: expected %d, got %d", tt.ahead, ahead)
			}
			if behind != tt.behind {
				t.Errorf("behind: expected %d, got %d", tt.behind, behind)
			}
		})
	}
}

// parseRevListOutput returns (ahead, behind) parsed from `git rev-list --left-right --count` output.
//
//nolint:gocritic // see doc comment for result order
func parseRevListOutput(out string) (int, int) {
	parts := strings.Fields(out)
	if len(parts) != 2 {
		return 0, 0
	}
	ahead, _ := strconv.Atoi(parts[0])  //nolint:errcheck // caller already validated numeric format
	behind, _ := strconv.Atoi(parts[1]) //nolint:errcheck // caller already validated numeric format

	return ahead, behind
}

func TestDetectRemoteProtocol(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "ssh shorthand", input: "git@github.com:owner/repo.git", expected: "ssh"},
		{name: "ssh url", input: "ssh://git@github.com/owner/repo.git", expected: "ssh"},
		{name: "https url", input: "https://github.com/owner/repo.git", expected: "https"},
		{name: "http url", input: "http://github.com/owner/repo.git", expected: "https"},
		{name: "empty string", input: "", expected: ""},
		{name: "unrecognized scheme", input: "file:///owner/repo", expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := vcs.DetectRemoteProtocol(tt.input)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestParseConfigList(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected map[string]string
	}{
		{
			name:     "single key",
			input:    "commit.gpgsign=true",
			expected: map[string]string{"commit.gpgsign": "true"},
		},
		{
			name:  "multiple keys",
			input: "commit.gpgsign=true\nuser.email=me@example.com",
			expected: map[string]string{
				"commit.gpgsign": "true",
				"user.email":     "me@example.com",
			},
		},
		{
			name:     "value containing equals",
			input:    "alias.l=log --pretty=format:%h",
			expected: map[string]string{"alias.l": "log --pretty=format:%h"},
		},
		{
			name:     "duplicate key keeps last",
			input:    "core.editor=vim\ncore.editor=nvim",
			expected: map[string]string{"core.editor": "nvim"},
		},
		{
			name:     "empty input",
			input:    "",
			expected: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := vcs.ParseConfigList(tt.input)
			if len(result) != len(tt.expected) {
				t.Fatalf("expected %d keys, got %d (%v)", len(tt.expected), len(result), result)
			}
			for key, value := range tt.expected {
				if result[key] != value {
					t.Errorf("key %q: expected %q, got %q", key, value, result[key])
				}
			}
		})
	}
}
