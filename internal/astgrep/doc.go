// Package astgrep runs the project's ast-grep convention rules and turns
// their findings into the trimmed shape a model is allowed to see.
// DESIGN.md's Structural rules section makes ast-grep the embedded
// structural engine for convention rules (gate order: formatter, native
// linter, ast-grep, type checker) and requires that only a finding's rule
// id, message, file:line, and fix hunk ever reach the model.
package astgrep
