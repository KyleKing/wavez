package vcs

import "errors"

// ErrNotJJRepo is returned when a wavez command runs against a directory
// jj cannot resolve to a repository. It is always wrapped with InitHint so
// the caller can surface an actionable message rather than fail obscurely
// or silently skip checkpointing.
var ErrNotJJRepo = errors.New("vcs: not a jj repository")

// InitHint is the command that turns a plain git repo, or a bare
// directory, into the colocated jj repo wavez checkpoints against.
const InitHint = "jj git init --colocate"
