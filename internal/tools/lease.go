package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/kyleking/wavez/internal/tool"
)

// Leases coordinates concurrent writes between threads. *lease.Manager
// satisfies it, and a tool built without one writes without coordinating.
type Leases interface {
	Acquire(ctx context.Context, target string) (func(), error)
}

// Checks reports what the harness's own gate runs already establish about
// the tree. *app.ChangeGate satisfies it, and a shell built without one
// runs every command the guard allows.
type Checks interface {
	// Status says what the gates already found for writer, the thread
	// asking. An empty writer is a caller with no thread, which gets the
	// answer about every writer.
	Status(writer string) (string, bool)
	// Covers reports whether the gates already ran, for writer, over every
	// package in pkgs, each a directory relative to the module root. An
	// empty writer covers what any writer changed.
	Covers(writer string, pkgs []string) bool
}

// Changes reports what the current run has written. *app.ChangeGate
// satisfies it, and a shell built without one runs a version-control
// command rather than answering it.
type Changes interface {
	// Changed lists what writer has written this run, most recent last. An
	// empty writer is a caller with no thread, which gets every writer's
	// changes, today's behavior.
	Changed(writer string) []tool.Change
}

// deps holds what a write tool may be given beyond its root and scope.
type deps struct {
	leases  Leases
	checks  Checks
	changes Changes
	symbols SymbolSearch
	// allowedCommands widen the guard's built-in list of shell commands that
	// run without a prompt, from what the project named.
	allowedCommands []string
	// extraRoots are the directories outside the project root a path may
	// resolve into, from what the project declared.
	extraRoots []string
}

// Option configures a tool's optional dependencies.
type Option func(*deps)

// WithSymbols lets a tool ask what the index declares. A tool that edits
// text does not need it to edit, and needs it to say whether a symbol tool
// could have done the job instead.
func WithSymbols(s SymbolSearch) Option {
	return func(d *deps) { d.symbols = s }
}

// WithLeases makes a tool hold the lease covering a write target's subtree
// for as long as the write takes. Acquisition sits here rather than at thread
// creation because a thread's directory set does not say where it writes.
func WithLeases(l Leases) Option {
	return func(d *deps) { d.leases = l }
}

// WithChecks lets a tool answer a command that re-runs the project's checks
// from what the gates already found, rather than running it. It is a
// dependency rather than a rule in the system prompt because the prompt has
// carried that rule since the gates shipped and 37 of 278 logged shell calls
// ran the checks anyway.
func WithChecks(c Checks) Option {
	return func(d *deps) { d.checks = c }
}

// WithChanges lets a tool answer a version-control command that only asks
// what this run has written, from what the harness recorded as it wrote it.
func WithChanges(c Changes) Option {
	return func(d *deps) { d.changes = c }
}

// WithAllowedCommands widens the guard's list of shell commands that run
// without asking. Everything not on it needs one approval, so a project's
// own toolchain is named here rather than left to a prompt per turn.
func WithAllowedCommands(names []string) Option {
	return func(d *deps) { d.allowedCommands = names }
}

// WithExtraRoots widens what a tool may reach to directories outside the
// project root. A path still resolves relative to the project root, and an
// absolute path is accepted only when it sits inside one of these.
func WithExtraRoots(dirs []string) Option {
	return func(d *deps) { d.extraRoots = dirs }
}

func newDeps(opts []Option) deps {
	var d deps
	for _, opt := range opts {
		opt(&d)
	}

	return d
}

// hold takes the lease covering target, returning a release func. A tool with
// no Leases holds nothing.
func (d deps) hold(ctx context.Context, target string) (func(), error) {
	if d.leases == nil {
		return func() {}, nil
	}

	release, err := d.leases.Acquire(ctx, target)
	if err != nil {
		return nil, fmt.Errorf("leasing %s: %w", target, err)
	}

	return release, nil
}

// holdAll takes the leases covering every target, in sorted order so two
// tools claiming overlapping sets cannot deadlock each other.
func (d deps) holdAll(ctx context.Context, targets []string) (func(), error) {
	sorted := append([]string(nil), targets...)
	sort.Strings(sorted)

	releases := make([]func(), 0, len(sorted))

	for _, t := range sorted {
		release, err := d.hold(ctx, t)
		if err != nil {
			for i := len(releases) - 1; i >= 0; i-- {
				releases[i]()
			}

			return nil, err
		}

		releases = append(releases, release)
	}

	return func() {
		for i := len(releases) - 1; i >= 0; i-- {
			releases[i]()
		}
	}, nil
}

// existingDirs keeps the targets whose parent directory is on disk, which is
// what separates a path from a command fragment that merely reads like one.
func existingDirs(targets []string) []string {
	out := make([]string, 0, len(targets))

	for _, t := range targets {
		if info, err := os.Stat(filepath.Dir(t)); err == nil && info.IsDir() {
			out = append(out, t)
		}
	}

	return out
}
