package gate

import (
	"path"
	"path/filepath"
	"strings"

	"github.com/kyleking/wavez/internal/tool"
)

// Attributable reports whether a failing Result is plausibly this run's own
// work, which is what separates a failure the model must fix from a tree it
// did not break.
//
// A failure carrying frames named a changed file by construction. One
// carrying only context named none, and the import graph decides it: a
// package that transitively imports a changed file's package is one this run
// could break without opening it, which is what a deleted function does to
// its callers, while a failure in a package no change reaches belongs to
// whatever else holds the working copy. Without a graph nothing can be ruled
// out, so every failure stays the run's.
func Attributable(r Result, graph *ImportGraph, changes []tool.Change) bool {
	if r.Pass {
		return true
	}

	for _, f := range r.Failures {
		if len(f.Frames) > 0 {
			return true
		}
	}

	if graph == nil {
		return true
	}

	reachable := reachablePackages(graph, changes)
	if len(reachable) == 0 {
		return true
	}

	for _, f := range r.Failures {
		for _, line := range f.Context {
			for _, m := range fileLineRe.FindAllStringSubmatch(line, -1) {
				if namesReachableFile(graph, reachable, m[1]) {
					return true
				}
			}
		}
	}

	return false
}

// reachablePackages is every package a change set could break: the packages
// holding the changed files and everything that transitively imports them. A
// changed file the graph does not know contributes nothing, since a file `go
// list` has never seen has no edges to follow.
func reachablePackages(graph *ImportGraph, changes []tool.Change) map[string]struct{} {
	out := map[string]struct{}{}

	for _, ch := range changes {
		pkg, ok := graph.FilePackage[filepath.ToSlash(filepath.Clean(ch.Path))]
		if !ok {
			continue
		}

		out[pkg] = struct{}{}

		for _, importer := range graph.transitiveImporters(pkg) {
			out[importer] = struct{}{}
		}
	}

	return out
}

// namesReachableFile reports whether reported, a path as some tool printed
// it, resolves to a file in a reachable package.
//
// `go test` prints a bare base name, so a base name is matched against every
// reachable package's base names and an ambiguous one reads as the run's. The
// cost of that reading is a model asked about a failure it may not own, where
// the other direction stops a run over one it does.
func namesReachableFile(graph *ImportGraph, reachable map[string]struct{}, reported string) bool {
	cleaned := filepath.ToSlash(filepath.Clean(reported))
	qualified := strings.Contains(cleaned, "/")

	for file, pkg := range graph.FilePackage {
		if _, ok := reachable[pkg]; !ok {
			continue
		}

		if qualified {
			if file == cleaned || strings.HasSuffix(file, "/"+cleaned) {
				return true
			}

			continue
		}

		if path.Base(file) == cleaned {
			return true
		}
	}

	return false
}
