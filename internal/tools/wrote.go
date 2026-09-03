package tools

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/kyleking/wavez/internal/tool"
)

// Tree names what version control reports as changed in the working copy.
// *vcs.Jj satisfies it, and a shell built without one records no change for
// a command that writes.
type Tree interface {
	WorkingCopyFiles(ctx context.Context, repoRoot string) ([]string, error)
}

// treeState is a fingerprint per changed path, taken either side of a
// command. A path absent from the earlier state was clean before it, and a
// path whose fingerprint moved was written during it.
type treeState map[string]string

// snapshot fingerprints every path version control calls changed. An error
// returns nil, which reads downstream as "nothing observed" and costs the
// run the attribution rather than the command.
func snapshot(ctx context.Context, t Tree, root string) treeState {
	if t == nil {
		return nil
	}

	paths, err := t.WorkingCopyFiles(ctx, root)
	if err != nil {
		return nil
	}

	state := make(treeState, len(paths))
	for _, rel := range paths {
		state[rel] = fingerprint(filepath.Join(root, rel))
	}

	return state
}

// fingerprint is size and modification time, which separates two contents
// of one path without reading it. A path that cannot be stat'd fingerprints
// as absent, which is what a deletion is.
func fingerprint(abs string) string {
	info, err := os.Stat(abs)
	if err != nil {
		return "absent"
	}

	return strconv.FormatInt(info.Size(), 10) + ":" +
		strconv.FormatInt(info.ModTime().UnixNano(), 10)
}

// wrote reports what a command changed, as the paths whose fingerprint moved
// between before and after. A path version control did not call changed
// either side is invisible here, which is correct for a build artifact and
// is the reason an ignored path is not reported as source.
//
// Line counts and ranges are left empty on purpose. Nothing observed here
// knows which lines moved, and a change with no ranges already falls back to
// package-level gate selection, where a guessed range would narrow it
// wrongly.
func wrote(before, after treeState) []tool.Change {
	if before == nil || after == nil {
		return nil
	}

	var paths []string

	for rel, now := range after {
		if was, seen := before[rel]; !seen || was != now {
			paths = append(paths, rel)
		}
	}

	for rel := range before {
		if _, still := after[rel]; !still {
			paths = append(paths, rel)
		}
	}

	sort.Strings(paths)

	changes := make([]tool.Change, 0, len(paths))
	for _, rel := range paths {
		changes = append(changes, tool.Change{Path: rel})
	}

	return changes
}
