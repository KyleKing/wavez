package gate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	listTimeout = time.Minute
	// The wholeModulePattern names every package under the root.
	wholeModulePattern = "./..."
	// The listedFields are the ones listedPackage decodes.
	listedFields = "ImportPath,Dir,GoFiles,CgoFiles,Imports,TestImports,XTestImports,Module"
)

// listedPackage is the part of `go list -json` the import graph needs.
//
//nolint:tagliatelle // the tags are go list's own field names
type listedPackage struct {
	Module       *listedModule `json:"Module"`
	ImportPath   string        `json:"ImportPath"`
	Dir          string        `json:"Dir"`
	GoFiles      []string      `json:"GoFiles"`
	CgoFiles     []string      `json:"CgoFiles"`
	Imports      []string      `json:"Imports"`
	TestImports  []string      `json:"TestImports"`
	XTestImports []string      `json:"XTestImports"`
}

//nolint:tagliatelle // the tag is go list's own field name
type listedModule struct {
	Path string `json:"Path"`
}

// importGraph is a module's packages by directory and who imports whom,
// tests included, since a test that imports a changed package is affected
// by the change.
type importGraph struct {
	// dirOf maps an import path to its directory relative to the root.
	dirOf map[string]string
	// pathOf is dirOf reversed.
	pathOf    map[string]string
	importers map[string][]string
	// testOnly holds the directories with no non-test source, which
	// `go build` refuses and `go test` accepts.
	testOnly map[string]bool
	module   string
}

// loadGraph builds the graph from one `go list -e -json ./...`. The -e keeps
// a package that does not parse in the listing, so a broken change still
// selects its importers. A non-empty reason means go could not list the
// module at all.
func loadGraph(ctx context.Context, goBin, root string) (importGraph, string) {
	out := run(ctx, invocation{
		dir: root, name: goBin, timeout: listTimeout,
		args: []string{"list", "-e", "-json=" + listedFields, wholeModulePattern},
	})
	if out.reason != "" {
		return importGraph{}, out.reason
	}

	if out.exit != 0 {
		return importGraph{}, oneLine(out.output)
	}

	graph := importGraph{
		dirOf: map[string]string{}, pathOf: map[string]string{},
		importers: map[string][]string{}, testOnly: map[string]bool{},
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(jsonObjects(out.output))))

	for {
		var pkg listedPackage

		err := dec.Decode(&pkg)
		if errors.Is(err, io.EOF) {
			return graph, ""
		}

		if err != nil {
			return importGraph{}, "go list printed something that is not a package list"
		}

		graph.add(root, pkg)
	}
}

// jsonObjects drops whatever go printed before the first object. With -e a
// warning can share the stream with the listing.
func jsonObjects(output string) string {
	if strings.HasPrefix(output, "{") {
		return output
	}

	if i := strings.Index(output, "\n{"); i >= 0 {
		return output[i+1:]
	}

	return ""
}

func (g *importGraph) add(root string, pkg listedPackage) {
	rel, err := filepath.Rel(root, pkg.Dir)
	if err != nil || pkg.Dir == "" {
		return
	}

	dir := filepath.ToSlash(rel)
	g.dirOf[pkg.ImportPath] = dir
	g.pathOf[dir] = pkg.ImportPath
	g.testOnly[dir] = len(pkg.GoFiles)+len(pkg.CgoFiles) == 0

	if pkg.Module != nil && g.module == "" {
		g.module = pkg.Module.Path
	}

	for _, imp := range slices.Concat(pkg.Imports, pkg.TestImports, pkg.XTestImports) {
		if !slices.Contains(g.importers[imp], pkg.ImportPath) {
			g.importers[imp] = append(g.importers[imp], pkg.ImportPath)
		}
	}
}

// scope is the packages one change touches, as the patterns go takes on its
// command line.
type scope struct {
	// all is every affected package, for go test.
	all []string
	// buildable leaves out the packages that hold only tests.
	buildable []string
}

// affected returns the packages a change in dirs touches: each directory's own
// package and every module package that imports one, directly or not.
// Patterns are ./-prefixed directories, because go resolves a bare
// `internal/x` against the standard library. A directory that is gone still
// selects whoever imported it. A directory go did not list (a nested module,
// or one whose every file a build constraint excludes) is left out, since
// naming it would fail the whole run on something go already declined.
func (g *importGraph) affected(dirs []string) scope {
	var queue []string

	for _, dir := range dirs {
		importPath, listed := g.pathOf[dir]
		if !listed {
			importPath = path.Join(g.module, dir)
		}

		queue = append(queue, importPath)
	}

	var (
		out  scope
		seen = map[string]bool{}
	)

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]

		if seen[cur] {
			continue
		}

		seen[cur] = true
		queue = append(queue, g.importers[cur]...)

		dir, ok := g.dirOf[cur]
		if !ok {
			continue
		}

		out.all = append(out.all, pattern(dir))
		if !g.testOnly[dir] {
			out.buildable = append(out.buildable, pattern(dir))
		}
	}

	slices.Sort(out.all)
	slices.Sort(out.buildable)

	return out
}

// dirsOf returns the distinct directories of files, in first-seen order.
func dirsOf(files []string) []string {
	var out []string

	for _, f := range files {
		if dir := path.Dir(f); !slices.Contains(out, dir) {
			out = append(out, dir)
		}
	}

	return out
}

// pattern is dir as go wants a package named on its command line.
func pattern(dir string) string {
	if dir == "." {
		return "."
	}

	return "./" + dir
}
