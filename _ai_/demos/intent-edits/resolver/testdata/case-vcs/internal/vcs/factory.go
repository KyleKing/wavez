package vcs

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/kyleking/gh-repo-dashboard/internal/models"
)

var jjAvailable = sync.OnceValue(func() bool {
	_, err := exec.LookPath("jj")

	return err == nil
})

// DetectVCSType inspects repoPath to determine whether it is a jj or git repository.
// A colocated repo (both .jj and .git) is treated as git when the jj binary is
// not on PATH, so repos stay usable instead of erroring on every jj call.
func DetectVCSType(repoPath string) models.VCSType {
	if _, err := os.Stat(filepath.Join(repoPath, ".jj")); err == nil {
		if jjAvailable() {
			return models.VCSTypeJJ
		}
		if _, err := os.Stat(filepath.Join(repoPath, ".git")); err == nil {
			return models.VCSTypeGit
		}

		return models.VCSTypeJJ
	}

	return models.VCSTypeGit
}

// GetOperations returns the Operations implementation matching repoPath's VCS type.
//
//nolint:ireturn // factory returns the vcs.Operations interface so callers can inject git or jj implementations
func GetOperations(repoPath string) Operations {
	vcsType := DetectVCSType(repoPath)
	switch vcsType {
	case models.VCSTypeJJ:
		return NewJJOperations()
	default:
		return NewGitOperations()
	}
}

// GetGitHubEnv returns extra environment variables needed for the gh CLI to work in a jj-colocated repo.
func GetGitHubEnv(repoPath string) []string {
	vcsType := DetectVCSType(repoPath)
	if vcsType == models.VCSTypeJJ {
		colocatedGit := filepath.Join(repoPath, ".git")
		if _, err := os.Stat(colocatedGit); err == nil {
			return nil
		}
		jjGit := filepath.Join(repoPath, ".jj", "repo", "store", "git")

		return []string{"GIT_DIR=" + jjGit}
	}

	return nil
}

// IsRepo reports whether path is a git or jj repository root.
func IsRepo(path string) bool {
	gitDir := filepath.Join(path, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		return true
	}

	jjDir := filepath.Join(path, ".jj")
	if _, err := os.Stat(jjDir); err == nil {
		return true
	}

	return false
}
