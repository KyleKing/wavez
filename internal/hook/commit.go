package hook

import (
	"context"
	"path"
	"slices"
	"strings"

	"github.com/kyleking/wavez/internal/session"
)

// recorders are the subcommands that put the working tree into history or
// send history elsewhere, by the program that has them. `jj git push` and
// `gh pr create`, which pushes the branch it opens a pull request for, are
// the ones that take two words.
var recorders = map[string][][]string{
	"git": {{"commit"}, {"push"}},
	"jj":  {{"commit"}, {"git", "push"}},
	"gh":  {{"pr", "create"}},
}

// wrappers are the programs that run the command given after them, and
// valued the options of git and jj that take a value of their own before
// the subcommand.
var (
	wrappers = []string{"env", "exec", "nohup", "timeout"}
	valued   = []string{
		"-C", "-c", "-R", "--repository", "--git-dir", "--work-tree", "--config", "--config-file", "--at-op",
		"--at-operation", "--color",
	}
	// The rehearsals are the options with which a command records nothing.
	rehearsals = []string{"--dry-run", "--help", "-h"}
)

// recordsHistory reports whether a shell command commits or pushes. A command that
// cannot be read all the way does not: holding one that is something else
// costs the agent a turn.
func recordsHistory(command string) bool {
	return recordsAt(command, 0)
}

func recordsAt(command string, depth int) bool {
	segments, ok := shellSegments(command)
	if !ok {
		return false
	}

	return slices.ContainsFunc(segments, func(words []string) bool {
		inv, ok := invocationOf(unwrapped(words))
		if !ok {
			return false
		}

		if script, ok := inv.shellScript(); ok && depth < maxShellDepth {
			return recordsAt(script, depth+1)
		}

		args := subcommand(inv.args)

		return !slices.ContainsFunc(args, func(arg string) bool { return slices.Contains(rehearsals, arg) }) &&
			slices.ContainsFunc(recorders[inv.prog], func(sub []string) bool { return startsWith(args, sub) })
	})
}

// unwrapped drops the programs that only run what follows them, with their
// own options, assignments, and the duration timeout takes.
func unwrapped(words []string) []string {
	for len(words) > 1 && slices.Contains(wrappers, path.Base(words[0])) {
		timed := path.Base(words[0]) == "timeout"
		words = words[1:]

		for len(words) > 1 && (strings.HasPrefix(words[0], "-") || isAssignment(words[0])) {
			words = words[1:]
		}

		if timed && len(words) > 1 {
			words = words[1:]
		}
	}

	return words
}

// subcommand drops the options a program takes before its subcommand, such as
// git's `-C dir` and `-c key=value` and jj's `-R dir`.
func subcommand(args []string) []string {
	for len(args) > 0 && len(args[0]) > 1 && args[0][0] == '-' {
		takesValue := slices.Contains(valued, args[0])
		args = args[1:]

		if takesValue && len(args) > 0 {
			args = args[1:]
		}
	}

	return args
}

func startsWith(words, prefix []string) bool {
	return len(words) >= len(prefix) && slices.Equal(words[:len(prefix)], prefix)
}

// committing answers a shell command that commits or pushes while the
// session's changes have not passed `wavez check`: it is denied once for
// that change set, with the command to run first. Where a commit hook exists
// this puts the targeted check ahead of the hook's whole-project one, and
// where none does, as under jj, it is the only check a commit meets.
//
// It has no way to fail. Anything it cannot establish lets the command
// through in silence, and so does a change set the checks could not run on,
// since asking again would not change that.
func (h *Handler) committing(ctx context.Context, ev Event, rec *record) (output, bool) {
	if !h.commitChecks || !recordsHistory(ev.ToolInput.Command) {
		return output{}, false
	}

	store := session.NewStore(rec.Root)

	state, ok := store.Read(ev.SessionID)
	if !ok {
		return output{}, false
	}

	changed, id, err := changeSet(ctx, store, rec.Root, state)
	if err != nil || len(changed) == 0 || slices.Contains([]string{state.Passed, state.Abstained, state.Asked}, id) {
		return output{}, false
	}

	// A hold that could not be recorded would repeat forever, so a failed
	// write holds nothing.
	_, err = store.Update(ctx, ev.SessionID, func(state *session.State) error {
		state.Asked = id

		return nil
	})
	if err != nil {
		rec.Note = "commit let through: " + err.Error()

		return output{}, false
	}

	rec.Note = "commit held: changes not checked"

	return deny("wavez: these changes have not passed `wavez check`. Run it and fix what it reports, " +
		"then send this command again. It will run if you send it again."), true
}
