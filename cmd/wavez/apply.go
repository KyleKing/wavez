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
	"github.com/kyleking/wavez/internal/session"
)

const applyUsage = `usage: wavez apply [--root DIR] [--no-check] < edits

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

	if err := flags.Parse(args); err != nil {
		return exitError(usageStatus)
	}

	root := *rootFlag
	if root == "" {
		root = hook.FindRoot(".")
	}

	text, err := io.ReadAll(io.LimitReader(streams.in, maxEdits))
	if err != nil {
		return fmt.Errorf("reading the edits: %w", err)
	}

	var (
		files   []string
		tracked bool
	)

	edits, err := apply.Parse(string(text))
	if err == nil {
		if files, err = apply.Apply(root, edits); err == nil {
			tracked = noted(ctx, root, files)
		}
	}

	if errors.Is(err, apply.ErrEdits) {
		sayf(streams.err, "wavez apply: %v\nNo file was changed.\n", err)

		return exitError(usageStatus)
	}

	if err != nil || *noCheck {
		return err //nolint:wrapcheck // the apply package names the file
	}

	// Inside a session the check covers everything the session changed, and
	// compares a failure with the tree it started from. Anywhere else it
	// covers the files just written.
	args = []string{"--root", root}
	if !tracked {
		for _, file := range files {
			args = append(args, filepath.Join(root, filepath.FromSlash(file)))
		}
	}

	return checkWith(ctx, "apply", args, streams, runQuick)
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
