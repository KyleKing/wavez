// Package stakes computes deterministic evidence about how costly a pending
// action would be to approve blind. It never returns a decision: approval
// stays with a permission.Gate (the user or a deterministic checker), per
// internal/permission's invariant that model output is not a policy input.
// A Score only enriches what a permission prompt shows and may raise the
// model tier that handles the thread afterward.
package stakes
