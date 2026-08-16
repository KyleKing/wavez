// Package gitstate resolves whether a file, and a specific piece of
// content within it, is still present in the current working tree.
//
// It deliberately reads working-tree state rather than git HEAD: a past
// decision may reference text an agent wrote that has since been staged
// or further edited but not committed, and uncommitted changes count as
// "current state" for the purposes of this analysis.
package gitstate

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Status is the resolved current-state verdict for a file referenced by a
// past decision.
type Status string

// Status values for FileState.
const (
	StatusLive       Status = "live"       // file exists, expected text is present
	StatusSuperseded Status = "superseded" // file exists, expected text is NOT present (rewritten since)
	StatusGone       Status = "gone"       // file no longer exists at this path
	StatusUnknown    Status = "unknown"    // couldn't determine (e.g. read error other than not-exist)
)

// FileState is the resolved current-state verdict for one file referenced
// by a past decision.
type FileState struct {
	Path        string
	Status      Status
	CurrentText string // truncated current file content, for LLM context; empty if Status is Gone/Unknown
	RenamedTo   string // best-effort rename detection target; empty if not detected or not applicable
}

// maxCurrentTextLen bounds how much of a file's content is retained in
// FileState.CurrentText, since it's headed into an LLM prompt and full
// file contents would blow up token budgets for large files.
const maxCurrentTextLen = 4000

// maxReadableFileSize bounds how large a file we'll read into memory to
// check for expected text. Anything larger is treated as StatusUnknown
// rather than risking an expensive read for a check that's unlikely to
// be meaningful on a huge file anyway.
const maxReadableFileSize = 1 << 20 // 1MB

// Resolve determines the current state of path (relative to projectPath)
// against expectedText (the content a past decision claims it left behind).
// It never returns an error for "file doesn't exist" — that's StatusGone,
// a normal outcome, not a failure. It only returns an error for something
// genuinely unexpected (e.g. projectPath itself doesn't exist).
func Resolve(ctx context.Context, projectPath, path, expectedText string) (FileState, error) {
	if _, err := os.Stat(projectPath); err != nil {
		return FileState{}, fmt.Errorf("checking project path %q: %w", projectPath, err)
	}

	state := FileState{Path: path}

	resolved, ok := safeJoin(projectPath, path)
	if !ok {
		state.Status = StatusUnknown
		return state, nil
	}

	info, err := os.Stat(resolved)
	switch {
	case os.IsNotExist(err):
		state.Status = StatusGone
		state.RenamedTo = detectRename(ctx, projectPath, path)

		return state, nil
	case err != nil:
		state.Status = StatusUnknown

		//nolint:nilerr // per spec, non-"not exist" read errors surface as StatusUnknown, not a Go error
		return state, nil
	}

	if info.Size() > maxReadableFileSize {
		state.Status = StatusUnknown
		return state, nil
	}

	//nolint:gosec // resolved is validated by safeJoin to stay within projectPath
	content, err := os.ReadFile(resolved)
	if err != nil {
		state.Status = StatusUnknown
		return state, nil //nolint:nilerr // per spec, read errors surface as StatusUnknown, not a Go error
	}

	text := string(content)
	state.CurrentText = truncate(text, maxCurrentTextLen)

	if expectedText == "" || strings.Contains(normalize(text), normalize(expectedText)) {
		state.Status = StatusLive
	} else {
		state.Status = StatusSuperseded
	}

	return state, nil
}

// safeJoin joins projectPath and path, rejecting any result that would
// escape projectPath (e.g. via "../"). A malformed path in old session
// data shouldn't be able to read outside the project, and shouldn't crash
// the caller either — the failure mode is reported via the returned bool.
func safeJoin(projectPath, path string) (string, bool) {
	projectAbs, err := filepath.Abs(projectPath)
	if err != nil {
		return "", false
	}

	joined := filepath.Clean(filepath.Join(projectAbs, path))

	rel, err := filepath.Rel(projectAbs, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}

	return joined, true
}

// normalize trims trailing whitespace per line and collapses repeated
// blank lines, since real files get reformatted (gofmt, prettier, etc.)
// and a byte-exact match would be too strict. It intentionally goes no
// further than whitespace normalization — semantic diffing is out of
// scope for this package.
func normalize(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	prevBlank := false
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t\r")
		blank := trimmed == ""
		if blank && prevBlank {
			continue
		}
		out = append(out, trimmed)
		prevBlank = blank
	}

	return strings.Join(out, "\n")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}

	return s[:n]
}

// detectRename shells out to git to best-effort locate a rename target
// for a file that no longer exists at path. It degrades silently (returns
// "") on any failure — not a git repo, git not installed, no rename found
// — because the vast majority of projects analyzed here either aren't git
// repos or simply won't have a detectable rename, and that's an expected,
// normal outcome rather than an error condition worth surfacing.
//
// It scans across all history (--all, no pathspec) rather than querying
// `git log -- path` directly: git only applies rename detection when
// walking history from a path that still exists in some ref, so querying
// with the old (now-deleted) path never surfaces the rename. Scanning all
// rename-type commits and matching the old path on the client side is the
// simplest reliable way to find it from the deleted side.
// RenameStatusLineFields is the number of tab-separated fields a `git log
// --name-status` rename line has: status, old path, new path.
const renameStatusLineFields = 3

func detectRename(ctx context.Context, projectPath, path string) string {
	cmd := exec.CommandContext(
		ctx, "git", "-C", projectPath,
		"log", "--all", "-M", "--diff-filter=R", "--name-status", "--format=",
	)

	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < renameStatusLineFields {
			continue
		}
		if !strings.HasPrefix(fields[0], "R") {
			continue
		}
		if fields[1] == path {
			return fields[2]
		}
	}

	return ""
}
