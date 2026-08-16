// Package vcs shells out to jj for the primitives an agent run needs: a
// repo's root, the files changed since an operation, a diff for a set of
// files, and per-turn checkpoint capture and restore built on jj's own
// operation log. DESIGN.md's VCS decision runs jj alone against a
// colocated repo, so there is no git backend and no library dependency.
package vcs
