package app

import "strings"

// BaseSystem is the instruction block every thread carries ahead of the
// project's own context. It stays short because the local model's served
// window is small, and it states only what the harness does differently from
// what a model assumes: gates decide when work is done, and the deterministic
// layer owns imports and formatting.
//
// How to use a tool belongs in that tool's own schema description, never
// here. Prose about tools in the system prompt measurably pushes a model
// into writing calls as prose: against `qwen3-coder-30b-a3b-instruct`
// through OpenRouter (August 2026, 15 samples per condition), a system
// prompt whose bullets described str_replace and search yielded a native
// tool call 0-4 times out of 15 while 3.5 KB of project documentation
// yielded 15 of 15, and the same request with no system prompt yielded 15
// of 15. What remains is caught by looksLikeToolCallText in internal/agent.
const BaseSystem = `You are wavez, a coding agent working in one repository.

How this harness works, which differs from what you may expect:

- Formatting and imports are fixed automatically after your edits. Never edit an
  import block and never adjust indentation to make code look right.
- Your work is checked by a build and by tests when you finish. Saying you are
  done does not end the task; passing those checks does. Do not claim success.
- Repeating a failed call unchanged ends the task. Change the arguments instead.
- Never silence a check to make it pass. Do not add a suppression comment, skip a
  test, or widen a timeout. Fix the cause, or report that you could not.`

func systemPrefix(context string) string {
	if strings.TrimSpace(context) == "" {
		return BaseSystem
	}

	return BaseSystem + "\n\n## Project context\n\n" + context
}
