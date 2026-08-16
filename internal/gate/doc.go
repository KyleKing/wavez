// Package gate implements DESIGN.md's v0.1 "Gates": change-triggered checks
// the model never asks for and never sees the internals of. A Runner
// debounces and coalesces tool.Change events into one run; Select resolves
// a run's changes to the narrowest test tier the code-intelligence store
// can support; RunGates executes the configured gates, serializing any pair
// that shares a resource; and a Log persists each gate's outcome.
package gate
