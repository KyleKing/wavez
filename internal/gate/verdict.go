package gate

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/kyleking/wavez/internal/config"
)

// verdictDir holds one empty entry per check run that passed, named for
// everything its answer depended on. Only a pass is kept: a failure or an
// abstention is always asked again.
const verdictDir = "verdicts"

// passed reports whether a pass is stored under key. The empty key is never
// one, since it is what a run whose inputs could not all be named gets.
func (c cache) passed(key string) bool {
	var entry struct{}

	return key != "" && c.load(verdictDir, key, &entry)
}

// pass stores a pass under key.
func (c cache) pass(key string) {
	if key != "" {
		c.store(verdictDir, key, struct{}{})
	}
}

// skippedTreeDir reports whether a directory is left out of the walk that
// finds a configured check's inputs: version control, wavez's own state,
// and installed packages, which a lockfile among the inputs stands for.
func skippedTreeDir(name string) bool {
	return name == ".git" || name == ".jj" || name == ".wavez" || name == "node_modules"
}

// tree lists the project's files once per run, for the configured checks
// whose cache key covers every file their globs and inputs match.
type tree struct {
	root  string
	files []string
	once  sync.Once
	ok    bool
}

// list returns every file under the root, slash-separated and sorted, and
// false when the walk failed.
func (t *tree) list() ([]string, bool) {
	t.once.Do(func() {
		err := filepath.WalkDir(t.root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if d.IsDir() && p != t.root && skippedTreeDir(d.Name()) {
				return filepath.SkipDir
			}

			if d.Type().IsRegular() || d.Type()&fs.ModeSymlink != 0 {
				t.files = append(t.files, filepath.ToSlash(strings.TrimPrefix(p, t.root+string(filepath.Separator))))
			}

			return nil
		})
		t.ok = err == nil
	})

	return t.files, t.ok
}

// customKey names a configured check's run by everything its answer can
// depend on: the check's commands, the files it is handed, PATH, and the
// bytes of every project file its globs or its declared inputs match. It is
// "" for a check that declared no inputs, whose command may read anything,
// and for one whose files could not all be read.
func customKey(proj project, c customCheck) string {
	if !c.check.Cacheable || proj.tree == nil {
		return ""
	}

	files, ok := proj.tree.list()
	if !ok {
		return ""
	}

	globs := slices.Concat(c.check.Globs, c.check.Inputs)
	parts := []string{
		"custom", c.check.Name, c.check.Command, c.check.Fix, os.Getenv("PATH"), strings.Join(c.files, "\x00"),
	}

	for _, f := range files {
		if !slices.ContainsFunc(globs, func(g config.Glob) bool { return g.Match(f) }) {
			continue
		}

		sum, ok := fileSum(proj.root, f)
		if !ok {
			return ""
		}

		parts = append(parts, f, sum)
	}

	return cacheKey(parts...)
}

// fileSum hashes one root-relative file, or the target a symlink names.
func fileSum(root, rel string) (string, bool) {
	full := filepath.Join(root, filepath.FromSlash(rel))

	info, err := os.Lstat(full)
	if err != nil {
		return "", false
	}

	if info.Mode()&fs.ModeSymlink != 0 {
		target, err := os.Readlink(full)

		return "link:" + target, err == nil
	}

	data, err := os.ReadFile(full) //nolint:gosec // a file of the project the checks were configured for
	if err != nil {
		return "", false
	}

	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:]), true
}

// cachedPass reports whether the check already passed on exactly these
// bytes.
func (r *runner) cachedPass(proj project, c *customCheck) bool {
	return r.cacheFor(proj.root).passed(customKey(proj, *c))
}

// toolID identifies a binary by where it is, its size, and when it was
// written, which changes when it is upgraded in place. It is "" when the
// binary cannot be read.
func toolID(bin string) string {
	info, err := os.Stat(bin)
	if err != nil {
		return ""
	}

	return bin + "\x00" + strconv.FormatInt(info.Size(), 10) + "\x00" + strconv.FormatInt(info.ModTime().UnixNano(), 10)
}

// goEnvKeys are the variables that change what the go tool builds, and with
// it what a linter type-checks.
var goEnvKeys = []string{
	"GOFLAGS", "GOOS", "GOARCH", "CGO_ENABLED", "GOEXPERIMENT", "GOTOOLCHAIN", "GOROOT", "GOPATH", "GOWORK",
}

// envParts returns the go and linter variables in a fixed order.
func envParts() []string {
	var out []string

	for _, key := range goEnvKeys {
		out = append(out, key+"="+os.Getenv(key))
	}

	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "GOLANGCI_") && !strings.HasPrefix(kv, lintCacheEnv+"=") {
			out = append(out, kv)
		}
	}

	slices.Sort(out)

	return out
}

// lintKeys names a lint run per package directory by what its findings in
// files can depend on: golangci-lint and go, their environment, the
// linter's configuration, the module files, the changed files in that
// directory, and the bytes of every file in it and in each module package
// it imports, its tests' imports included. A directory the graph does not
// hold gets no key, and neither does any directory when the module reaches
// code the graph cannot see: a vendor directory, a go.work, or a replace
// with a local path.
func lintKeys(root, lintBin, goBin string, g importGraph, files []string) map[string]string {
	keys := map[string]string{}
	if !moduleSelfContained(root) {
		return keys
	}

	shared := slices.Concat([]string{"lint", toolID(lintBin), toolID(goBin)}, envParts())

	for _, name := range []string{
		".golangci.toml", ".golangci.yml", ".golangci.yaml", ".golangci.json", "go.mod", "go.sum",
	} {
		sum, _ := fileSum(root, name)
		shared = append(shared, name, sum)
	}

	sums := map[string]string{}

	for _, dir := range dirsOf(files) {
		deps, ok := g.closure(dir)
		if !ok {
			continue
		}

		parts := slices.Concat(shared, []string{dir}, filesIn(dir, files))

		for _, dep := range deps {
			if _, seen := sums[dep]; !seen {
				sums[dep] = dirSum(root, dep)
			}

			parts = append(parts, dep, sums[dep])
		}

		keys[dir] = cacheKey(parts...)
	}

	return keys
}

// filesIn returns the files directly in dir, sorted.
func filesIn(dir string, files []string) []string {
	var out []string

	for _, f := range files {
		if path.Dir(f) == dir {
			out = append(out, f)
		}
	}

	slices.Sort(out)

	return out
}

// dirSum hashes the names and bytes of every file directly in dir, which
// covers its Go source of every build tag, its cgo sources, and the files it
// embeds from beside it.
func dirSum(root, dir string) string {
	full := filepath.Join(root, filepath.FromSlash(dir))

	entries, err := os.ReadDir(full)
	if err != nil {
		return "unreadable"
	}

	sum := sha256.New()

	for _, e := range entries {
		if e.IsDir() {
			continue
		}

		hash, ok := fileSum(root, path.Join(dir, e.Name()))
		if !ok {
			hash = "unreadable"
		}

		sum.Write([]byte(e.Name() + "\x00" + hash + "\x00"))
	}

	return hex.EncodeToString(sum.Sum(nil))
}

// moduleSelfContained reports whether every package a lint run type-checks
// is either in the module's own tree or pinned by go.sum.
func moduleSelfContained(root string) bool {
	if gowork := os.Getenv("GOWORK"); gowork != "" && gowork != "off" {
		return false
	}

	if isDir(filepath.Join(root, "vendor")) {
		return false
	}

	for dir := root; ; dir = filepath.Dir(dir) {
		if isFile(filepath.Join(dir, "go.work")) && os.Getenv("GOWORK") != "off" {
			return false
		}

		if filepath.Dir(dir) == dir {
			break
		}
	}

	mod, err := os.ReadFile(filepath.Join(root, "go.mod")) //nolint:gosec // the project's own go.mod
	if err != nil {
		return false
	}

	for line := range strings.SplitSeq(string(mod), "\n") {
		_, target, found := strings.Cut(line, "=>")
		if target = strings.TrimSpace(target); found && strings.HasPrefix(target, ".") || path.IsAbs(target) {
			return false
		}
	}

	return true
}

func isDir(name string) bool {
	info, err := os.Stat(name)

	return err == nil && info.IsDir()
}
