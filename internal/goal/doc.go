// Package goal checks a written goal with code. A person, or the agent when
// asked, lists the conditions that make a task done in .wavez/goal.toml, and
// this package says which hold: a test that runs and passes, a pattern
// found in a file or found in none, a path that exists, a command that
// exits 0. No model judges anything.
//
// A condition that could not be checked abstains. A missing binary, a
// timeout, or a toolchain error is "could not check", never "not done", and
// it neither blocks the end of a turn nor reaches the agent.
//
// A command condition runs arbitrary shell, which is the trust a
// .wavez.toml check already has. The difference is where the file lives:
// .wavez/ ignores itself in git, so a repository cannot ship a goal to
// whoever clones it. The goal file must stay there for that reason. Every
// other path a goal names is held inside the project root, through symlinks
// too.
package goal
