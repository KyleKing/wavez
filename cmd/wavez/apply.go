package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/kyleking/wavez/internal/apply"
	"github.com/kyleking/wavez/internal/hook"
	"github.com/kyleking/wavez/internal/lane"
	"github.com/kyleking/wavez/internal/session"
	"github.com/kyleking/wavez/internal/warm"
)

const applyUsage = `usage: wavez apply [--root DIR] [--at REV] [--no-check] < edits

Makes every edit read from stdin, or none of them, then checks the result as
wavez check does. An edit is the lines to replace and their replacement:

=== path/from/the/project/root.go
%OPEN%
exact lines to replace
%SPLIT%
their replacement
%CLOSE%

Nothing above the ======= appends to the file, and creates it when it is not
there. Several edits may follow one === line, and several files one another.

With --at REV the edits are made and checked in a second copy of the tree at
that revision, kept outside the checkout, which is left as it is: a fix for
a change that is out for review while the checkout has moved on.
`

// maxEdits bounds what is read from stdin.
const maxEdits = 4 << 20

// runApply makes the edits on stdin and checks what they left: one command
// for what is otherwise a read, an edit, and a test run per file. The edits
// are all made or none is, and an edit that does not apply says which and
// why and exits 2, the status of a command that was not understood.
func runApply(ctx context.Context, args []string, streams stdio) error {
	flags := flag.NewFlagSet("apply", flag.ContinueOnError)
	flags.SetOutput(streams.err)
	flags.Usage = func() { sayf(streams.err, "%s", apply.Fill(applyUsage)) }

	rootFlag := flags.String("root", "", "project root (default: the project the working directory is in)")
	noCheck := flags.Bool("no-check", false, "make the edits and check nothing")
	at := flags.String("at", "", "make the edits in a second copy of the tree at this revision, not in the checkout")

	if err := flags.Parse(args); err != nil {
		return exitError(usageStatus)
	}

	root, inLane, err := applyRoot(ctx, *rootFlag, *at, streams)
	if err != nil {
		return err
	}

	files, tracked, err := written(ctx, root, inLane, streams)
	if errors.Is(err, apply.ErrEdits) {
		sayf(streams.err, "wavez apply: %v\nNo file was changed.\n", err)

		return exitError(usageStatus)
	}

	if err != nil || *noCheck {
		return err
	}

	return applyChecked(ctx, root, files, tracked, inLane, streams)
}

// applyRoot is where the edits go: the project, or with a revision the
// lane at it, which the returned flag says it is. A lane is not the
// session's tree: what is made there is checked as the files written and
// compared with the lane's own revision.
func applyRoot(ctx context.Context, root, at string, streams stdio) (string, bool, error) {
	if root == "" {
		root = hook.FindRoot(".")
	}

	if at == "" {
		return root, false, nil
	}

	opened, err := openLane(ctx, root, at)
	if err != nil {
		return "", false, err
	}

	sayf(streams.err, "wavez apply: editing %s, at %s; the checkout is left as it is. "+
		"Commit and push from there with git -C.\n", opened.Top, at)

	return opened.Root, true, nil
}

// written makes the edits on stdin under root and returns the files they
// wrote and whether a session was told of them.
func written(ctx context.Context, root string, inLane bool, streams stdio) ([]string, bool, error) {
	text, err := io.ReadAll(io.LimitReader(streams.in, maxEdits))
	if err != nil {
		return nil, false, fmt.Errorf("reading the edits: %w", err)
	}

	edits, err := apply.Parse(string(text))
	if err != nil {
		return nil, false, err //nolint:wrapcheck // the apply package's error is the message
	}

	files, err := apply.Apply(root, edits)
	if err != nil {
		return nil, false, err //nolint:wrapcheck // the apply package names the file
	}

	return files, !inLane && noted(ctx, root, files), nil
}

// applyChecked checks what the edits left. Inside a session the check
// covers everything the session changed, and compares a failure with the
// tree it started from. Anywhere else it covers the files just written.
func applyChecked(ctx context.Context, root string, files []string, tracked, inLane bool, streams stdio) error {
	args := []string{"--root", root}
	if !tracked {
		for _, file := range files {
			args = append(args, filepath.Join(root, filepath.FromSlash(file)))
		}
	}

	kind := runQuick
	if inLane {
		kind = runLane
	}

	// A build started when the prompt arrived is compiling what this check
	// is about to ask for, and two compilers at once finish later than one.
	if id := os.Getenv(hook.SessionEnv); tracked && id != "" {
		warm.Wait(ctx, root, id)
	}

	err := checkWith(ctx, "apply", args, streams, kind)

	// What the quick check left out is started now, where the project has
	// background runs on, so that it is done by the time it is asked for.
	if err == nil && tracked && projectConfig(root).Warm {
		if warmErr := warmStart(root, os.Getenv(hook.SessionEnv)); warmErr != nil {
			sayf(streams.err, "wavez apply: the rest of the checks were not started: %v\n", warmErr)
		}
	}

	return err
}

// noted records the files in the session the command runs in, as an edit
// tool's write is recorded, and reports whether there is such a session.
func noted(ctx context.Context, root string, files []string) bool {
	id := os.Getenv(hook.SessionEnv)

	resolved, err := filepath.Abs(root)
	if id == "" || err != nil {
		return false
	}

	store := session.NewStore(resolved)
	if _, ok := store.Read(id); !ok {
		return false
	}

	// A write that is not recorded is still found by comparing the tree
	// with where the session started, wherever that can be done.
	_, _ = store.Update(ctx, id, func(state *session.State) error { //nolint:errcheck // see above
		for _, file := range files {
			state.Record(file)
		}

		return nil
	})

	return true
}

// openLane opens the second copy of the tree at rev for the project at
// root, under the user's cache directory.
func openLane(ctx context.Context, root, rev string) (lane.Lane, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return lane.Lane{}, fmt.Errorf("finding the cache directory: %w", err)
	}

	opened, err := lane.Open(ctx, root, filepath.Join(cache, "wavez", "lanes"), rev)
	if err != nil {
		return lane.Lane{}, fmt.Errorf("opening a lane at %s: %w", rev, err)
	}

	return opened, nil
}
