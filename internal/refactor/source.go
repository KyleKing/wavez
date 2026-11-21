package refactor

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const goExt = ".go"

// parsedFile is one Go file with its bytes and syntax tree, known by its
// slash-separated path under the module root, which is also how it is
// reported.
type parsedFile struct {
	fset *token.FileSet
	tok  *token.File
	ast  *ast.File
	rel  string
	src  []byte
}

func (p *parsedFile) offset(pos token.Pos) int { return p.tok.Offset(pos) }

func (p *parsedFile) line(pos token.Pos) int { return p.tok.Line(pos) }

// lineAt is the line holding a byte offset.
func (p *parsedFile) lineAt(off int) int { return p.tok.Line(p.tok.Pos(off)) }

// lineEnd is the offset of the newline ending the line that holds off, or
// the end of the file.
func (p *parsedFile) lineEnd(off int) int {
	if i := bytes.IndexByte(p.src[off:], '\n'); i >= 0 {
		return off + i
	}

	return len(p.src)
}

func parseSource(rel string, src []byte) (*parsedFile, error) {
	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, rel, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", rel, err)
	}

	return &parsedFile{fset: fset, tok: fset.File(file.Pos()), ast: file, rel: rel, src: src}, nil
}

// tree reads and parses files under the module root, each at most once.
// It is confined to the root by os.Root, so no path a caller supplies can
// reach outside the module.
type tree struct {
	fsys  *os.Root
	cache map[string]*parsedFile
	dir   string
}

func (t *tree) read(rel string) ([]byte, error) {
	data, err := t.fsys.ReadFile(filepath.FromSlash(rel))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", rel, err)
	}

	return data, nil
}

func (t *tree) parse(rel string) (*parsedFile, error) {
	if pf, ok := t.cache[rel]; ok {
		return pf, nil
	}

	src, err := t.read(rel)
	if err != nil {
		return nil, err
	}

	pf, err := parseSource(rel, src)
	if err != nil {
		return nil, err
	}

	t.cache[rel] = pf

	return pf, nil
}

// exists reports whether rel names something under the root, and whether
// that something is a directory.
func (t *tree) exists(rel string) (bool, bool) {
	info, err := t.fsys.Stat(filepath.FromSlash(rel))
	if err != nil {
		return false, false
	}

	return true, info.IsDir()
}

// goFilesIn lists the Go files directly inside a directory, sorted.
func (t *tree) goFilesIn(relDir string) ([]string, error) {
	dir, err := t.fsys.Open(filepath.FromSlash(relDir))
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", relDir, err)
	}

	entries, err := dir.ReadDir(-1)
	closeErr := dir.Close()

	if err = errors.Join(err, closeErr); err != nil {
		return nil, fmt.Errorf("listing %s: %w", relDir, err)
	}

	var out []string

	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), goExt) {
			out = append(out, path.Join(relDir, e.Name()))
		}
	}

	sort.Strings(out)

	return out, nil
}

// parseDir parses every Go file directly inside a directory, skipping any
// that does not parse: a broken neighbor must not stop a search that the
// file asked about can still answer.
func (t *tree) parseDir(relDir string) ([]*parsedFile, error) {
	names, err := t.goFilesIn(relDir)
	if err != nil {
		return nil, err
	}

	out := make([]*parsedFile, 0, len(names))

	for _, name := range names {
		pf, parseErr := t.parse(name)
		if parseErr != nil {
			continue
		}

		out = append(out, pf)
	}

	return out, nil
}

// walkGoFiles lists every Go file of the module, leaving out what the go
// tool leaves out (dot and underscore directories, testdata, vendor) and
// any nested module.
func (t *tree) walkGoFiles() ([]string, error) {
	var out []string

	err := fs.WalkDir(t.fsys.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			if p != "." && t.skipDir(p, d.Name()) {
				return fs.SkipDir
			}

			return nil
		}

		if d.Type().IsRegular() && strings.HasSuffix(p, goExt) {
			out = append(out, p)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking %s: %w", t.dir, err)
	}

	return out, nil
}

func (t *tree) skipDir(rel, name string) bool {
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" || name == "vendor" {
		return true
	}

	found, _ := t.exists(path.Join(rel, "go.mod"))

	return found
}

// modulePath reads the module line of go.mod, which is what tells a local
// import from a third-party one.
func (t *tree) modulePath() string {
	data, err := t.read("go.mod")
	if err != nil {
		return ""
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module"); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`)
		}
	}

	return ""
}
