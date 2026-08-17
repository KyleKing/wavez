package gate

import (
	"context"
	"path/filepath"
)

// ImportGraph is a Go package import graph built from `go list`, used for
// the importer-level selection tier. Nothing in this package writes it to
// codeintel's edges table (v0.1 ships no edges writer at all); it lives
// only for the duration of one Select call or Runner's cache of it.
type ImportGraph struct {
	// FilePackage maps a repo-relative file path to the import path of the
	// package that declares it.
	FilePackage map[string]string
	importers   map[string][]string
}

// NewImportGraph builds an ImportGraph directly from a file-to-package map
// and a package-to-direct-importers map, for callers that already have a
// graph (a persisted one, or a fixture in a test) rather than a repoRoot to
// run `go list` against.
func NewImportGraph(filePackage map[string]string, importers map[string][]string) *ImportGraph {
	return &ImportGraph{FilePackage: filePackage, importers: importers}
}

// BuildImportGraph runs `go list -json ./...` in repoRoot and builds the
// file-to-package map and the reverse (importer) edges Select needs.
func BuildImportGraph(ctx context.Context, repoRoot string) (*ImportGraph, error) {
	pkgs, err := listGoPackages(ctx, repoRoot)
	if err != nil {
		return nil, err
	}

	graph := &ImportGraph{
		FilePackage: make(map[string]string),
		importers:   make(map[string][]string),
	}
	for i := range pkgs {
		graph.addPackage(repoRoot, &pkgs[i])
	}

	return graph, nil
}

func (g *ImportGraph) addPackage(repoRoot string, pkg *goPackage) {
	files := make([]string, 0, len(pkg.GoFiles)+len(pkg.TestGoFiles)+len(pkg.XTestGoFiles))
	files = append(files, pkg.GoFiles...)
	files = append(files, pkg.TestGoFiles...)
	files = append(files, pkg.XTestGoFiles...)

	for _, f := range files {
		rel, err := filepath.Rel(repoRoot, filepath.Join(pkg.Dir, f))
		if err != nil {
			continue
		}

		g.FilePackage[filepath.ToSlash(rel)] = pkg.ImportPath
	}

	for _, imp := range pkg.Imports {
		g.importers[imp] = append(g.importers[imp], pkg.ImportPath)
	}
}

// transitiveImporters returns every package that transitively imports pkg,
// excluding pkg itself.
func (g *ImportGraph) transitiveImporters(pkg string) []string {
	visited := make(map[string]struct{})
	queue := []string{pkg}

	var out []string

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		for _, importer := range g.importers[cur] {
			if _, ok := visited[importer]; ok {
				continue
			}

			visited[importer] = struct{}{}

			out = append(out, importer)
			queue = append(queue, importer)
		}
	}

	return out
}
