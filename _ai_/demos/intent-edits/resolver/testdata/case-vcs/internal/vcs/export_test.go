package vcs

import "context"

// WithCommandRunner exposes withCommandRunner to black-box tests for stubbing
// git/jj CLI calls.
var WithCommandRunner = withCommandRunner

// SetJJAvailableForTest forces jj-binary detection for black-box tests and
// returns a restore func. Only safe from tests that have not called t.Parallel.
func SetJJAvailableForTest(available bool) func() {
	prev := jjAvailable
	jjAvailable = func() bool { return available }

	return func() { jjAvailable = prev }
}

// CommandRunner exposes the commandRunner function type to black-box tests.
type CommandRunner = commandRunner

// RunCommand exposes the unexported runCommand helper to black-box tests.
var RunCommand = runCommand

// RunGitForTest exposes GitOperations.runGit to black-box tests.
func (g *GitOperations) RunGitForTest(ctx context.Context, repoPath string, args ...string) (string, error) {
	return g.runGit(ctx, repoPath, args...)
}

// RunJJForTest exposes JJOperations.runJJ to black-box tests.
func (j *JJOperations) RunJJForTest(ctx context.Context, repoPath string, args ...string) (string, error) {
	return j.runJJ(ctx, repoPath, args...)
}

// JJBookmark exposes the unexported jjBookmark type to black-box tests.
type JJBookmark = jjBookmark

// NewJJBookmark constructs a jjBookmark for black-box tests, since its fields are unexported.
func NewJJBookmark(name, upstream string, ahead, behind int, head string) JJBookmark {
	return jjBookmark{name: name, upstream: upstream, ahead: ahead, behind: behind, head: head}
}

// ParseJJBookmarkList exposes the unexported parseJJBookmarkList helper to black-box tests.
var ParseJJBookmarkList = parseJJBookmarkList

// JJ CLI template format strings, exposed for black-box exec tests that assert
// on the exact template argument passed to the jj command.
const (
	JJCommitLineFormat      = jjCommitLineFormat
	JJCurrentBookmarkFormat = jjCurrentBookmarkFormat
	JJBookmarkListFormat    = jjBookmarkListFormat
	JJWorkspaceListFormat   = jjWorkspaceListFormat
)

// DetectRemoteProtocol exposes the unexported detectRemoteProtocol helper to black-box tests.
var DetectRemoteProtocol = detectRemoteProtocol

// ParseConfigList exposes the unexported parseConfigList helper to black-box tests.
var ParseConfigList = parseConfigList
