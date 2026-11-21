// Package refactor implements the edits that take a name instead of text:
// rename, move, and delete for Go declarations.
package refactor

// Skill is the Claude Code skill (a SKILL.md with frontmatter) that tells an
// agent when to reach for these commands. `wavez init` installs it, and the
// refactor lane replaces this placeholder with an embedded file.
const Skill = "---\nname: wavez-refactor\ndescription: placeholder\n---\n"
