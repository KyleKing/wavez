// Package vcs shells out to git for the primitives gates need: a repo's
// root, the files that changed since a marker, and a diff for a set of
// files. It is the git-only slice of DESIGN.md's v0.4 Operations interface,
// kept to what the v0.1 gate actually consumes; a jj backend is a later
// package, not a wider interface here.
package vcs
