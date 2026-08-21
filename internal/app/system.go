package app

import "strings"

// BaseSystem is the instruction block every thread carries ahead of the
// project's own context. It stays short because the local model's served
// window is small, and it states only what the harness does differently from
// what a model assumes: gates decide when work is done, and the deterministic
// layer owns imports and formatting.
//
// What it says about the harness has to be here rather than in the project's
// own context list, because a project's contributor guide is written for a
// human with a shell and a git remote and reads as an instruction to
// reproduce the whole CI. Measured on this repo before that entry was
// dropped: 40 of 261 logged shell calls ran what the gates already run, two
// of them going on to `git add -A && git commit`, and every one came from a
// headless run.
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
- Do not run this project's CI or its version control: mise, hk, golangci-lint,
  gofmt, git, and jj are the harness's job, and a call to one spends a turn and
  changes nothing. Running a single test to watch it fail is not that.
- Repeating a failed call unchanged ends the task. Change the arguments instead.
- Never silence a check to make it pass. Do not add a suppression comment, skip a
  test, or widen a timeout. Fix the cause, or report that you could not.`

// PlanSystem replaces the checks paragraph of BaseSystem for a plan thread,
// whose registry holds no editing tool. Without it a plan run has no
// finish line: measured on qwen3:8b asked to rename a constant, it made 30
// searches and 25 reads across 57 turns and stopped only on the spend
// ceiling, never attempting an edit and never producing a plan.
//
// It says nothing about which tools exist, for the reason BaseSystem gives.
const PlanSystem = `You are wavez, planning a change in one repository. You cannot edit
anything on this thread, and nothing you describe will be applied.

Read enough to be specific, then write the plan and stop. A plan names the files to
change, what changes in each, and what would prove it worked. Say plainly where you are
unsure rather than reading further to be certain.`

func systemPrefix(context string) string {
	return withContext(BaseSystem, context)
}

func planSystemPrefix(context string) string {
	return withContext(PlanSystem, context)
}

func withContext(system, context string) string {
	if strings.TrimSpace(context) == "" {
		return system
	}

	return system + "\n\n## Project context\n\n" + context
}
