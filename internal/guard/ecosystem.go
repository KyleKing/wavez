package guard

import (
	"sort"
	"strings"
)

// ecosystems name the programs one language's toolchain runs, so a project
// declares cmdPython once instead of answering a prompt per variant of
// `uv run ruff check`. A permission answer is keyed on the whole command
// line by design, which is what makes a bare program name the only thing
// that generalizes.
//
// A bundle is an ergonomic control and not a security one. Every program
// here reads a script from a file or an argument, so allowing it hands the
// classifier a string it never reads. What stops that is the Seatbelt
// profile, which denies the protected paths and everything outside the
// project on the syscall regardless of which program made it.
var ecosystems = map[string][]string{
	"go": {"gopls", "gotestsum", "staticcheck"},
	"javascript": {
		"bun", "bunx", "deno", "eslint", "node", "npm", "npx", "pnpm",
		"prettier", "tsc", "tsx", "vitest", "yarn",
	},
	cmdPython: {
		"basedpyright", "mypy", cmdPip, "poetry", "py.test", "pyright", "pytest",
		cmdPython, "python3", "ruff", "ty", "uv", "uvx",
	},
	cmdRuby: {"bundle", "gem", "rake", "rspec", "rubocop", cmdRuby},
	"rust":  {cmdCargo, "clippy-driver", "rustc", "rustfmt"},
	"zig":   {"zig"},
}

// EcosystemCommands returns the programs the named ecosystems run without a
// prompt, sorted and deduplicated. A name no bundle covers contributes
// nothing, because the config schema is a union of the names and rejects a
// typo before it ever reaches here.
func EcosystemCommands(names []string) []string {
	seen := map[string]bool{}
	out := []string{}

	for _, name := range names {
		for _, cmd := range ecosystems[strings.ToLower(strings.TrimSpace(name))] {
			if seen[cmd] {
				continue
			}

			seen[cmd] = true

			out = append(out, cmd)
		}
	}

	sort.Strings(out)

	return out
}
