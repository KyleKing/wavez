// Package lean is the cut-down Claude Code session wavez starts and
// measures: the harness's own system prompt replaced by a few sentences,
// and its tools narrowed to the ones such a session uses. Every turn reads
// the system prompt and the tool descriptions back, and on the benchmark's
// tasks that standing text was half of what a session cost.
package lean

import "slices"

const (
	// Prompt stands in for the harness's own system prompt.
	Prompt = "You are a coding agent working in a git checkout from a terminal. " +
		"Make the change or answer the question you are given, check it, and stop. " +
		"Run independent commands and reads in one message. Say what you did in a sentence or two."
	// Tools are the built-in tools the session keeps.
	Tools = "Bash,Read,Edit,Grep,Glob"

	promptFlag = "--system-prompt"
	toolsFlag  = "--tools"
)

// Args returns the arguments for `claude` that make a session lean, followed
// by rest. A flag rest already sets is left to rest: somebody who names
// their own tools or prompt meant it.
func Args(rest ...string) []string {
	var args []string

	if !sets(rest, promptFlag, "--system-prompt-file") {
		args = append(args, promptFlag, Prompt)
	}

	if !sets(rest, toolsFlag) {
		args = append(args, toolsFlag, Tools)
	}

	return append(args, rest...)
}

// sets reports whether args carries one of flags, as `--flag value` or as
// `--flag=value`.
func sets(args []string, flags ...string) bool {
	return slices.ContainsFunc(args, func(arg string) bool {
		return slices.ContainsFunc(flags, func(flag string) bool {
			return arg == flag || len(arg) > len(flag) && arg[:len(flag)+1] == flag+"="
		})
	})
}
