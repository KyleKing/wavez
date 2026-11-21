package refactor

import _ "embed"

// Skill is the Claude Code skill (a SKILL.md with frontmatter) that tells an
// agent when to reach for these commands. `wavez init` installs it.
//
//go:embed SKILL.md
var Skill string
