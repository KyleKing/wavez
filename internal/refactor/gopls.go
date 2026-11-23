package refactor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// maxParallel bounds how many gopls processes run at once. Each one loads
// the package graph, so more than a few only trade wall time for memory.
const maxParallel = 4

// toolchain runs the programs a refactor shells out to, from the module
// root.
type toolchain struct {
	dir    string
	gopls  string
	goTool string
}

// commandError is a program that ran and exited non-zero, with what it
// printed.
type commandError struct {
	output string
}

func (e *commandError) Error() string { return e.output }

// run executes a program and returns its standard output. A missing program
// comes back as ToolMissingError and a failing one as commandError carrying
// both streams, so callers decide what the failure means.
func (t toolchain) run(ctx context.Context, program string, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer

	//nolint:gosec // the program is gopls or go, and the arguments are paths and names this package resolved
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Dir = t.dir
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		return stdout.String(), nil
	}

	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", fmt.Errorf("running %s: %w", program, ctxErr)
	}

	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return "", &commandError{output: strings.TrimSpace(stderr.String() + stdout.String())}
	}

	return "", &ToolMissingError{Program: program}
}

func (t toolchain) runGopls(ctx context.Context, op string, args ...string) (string, error) {
	out, err := t.run(ctx, t.gopls, args...)

	var failed *commandError
	if errors.As(err, &failed) {
		return "", &GoplsError{Op: op, Reason: t.relativize(strings.TrimPrefix(failed.output, "gopls: "))}
	}

	return out, err
}

// relativize strips the module root from paths in a tool's output, which
// is most of its length and none of its meaning.
func (t toolchain) relativize(text string) string {
	return strings.ReplaceAll(text, t.dir+string(filepath.Separator), "")
}

func position(rel string, offset int) string {
	return filepath.FromSlash(rel) + ":#" + strconv.Itoa(offset)
}

// rename asks gopls to rename the identifier at a position everywhere it is
// used, writing the files, and returns the files it changed. That reaches
// what gopls loaded, which is the default build: see session.excluded.
func (t toolchain) rename(ctx context.Context, at, to string) ([]string, error) {
	out, err := t.runGopls(ctx, "rename", "rename", "-l", "-w", at, to)
	if err != nil {
		return nil, err
	}

	var files []string

	for line := range strings.SplitSeq(t.relativize(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			files = append(files, filepath.ToSlash(line))
		}
	}

	return files, nil
}

// implementations lists the methods inside the module that implement the
// interface method at an offset, each at its name.
func (t toolchain) implementations(ctx context.Context, rel string, offset int) ([]location, error) {
	out, err := t.runGopls(ctx, "implementation search", "implementation", position(rel, offset))
	if err != nil {
		return nil, err
	}

	return t.locations(out), nil
}

// builtFilesFormat prints one line per Go file of a package the build
// holds: its directory, a tab, and its name.
const builtFilesFormat = `{{$dir := .Dir}}{{range .GoFiles}}{{$dir}}{{"\t"}}{{.}}{{"\n"}}{{end}}` +
	`{{range .CgoFiles}}{{$dir}}{{"\t"}}{{.}}{{"\n"}}{{end}}` +
	`{{range .TestGoFiles}}{{$dir}}{{"\t"}}{{.}}{{"\n"}}{{end}}` +
	`{{range .XTestGoFiles}}{{$dir}}{{"\t"}}{{.}}{{"\n"}}{{end}}`

// builtFiles is the Go files of the module that the default build holds,
// tests included. The rest are left out by a file name suffix, a build
// constraint, or cgo being off.
func (t toolchain) builtFiles(ctx context.Context) (map[string]bool, error) {
	out, err := t.run(ctx, t.goTool, "list", "-e", "-f", builtFilesFormat, "./...")
	if err != nil {
		return nil, err
	}

	built := map[string]bool{}

	for line := range strings.SplitSeq(out, "\n") {
		dir, name, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}

		if rel, relErr := filepath.Rel(t.dir, dir); relErr == nil {
			built[path.Join(filepath.ToSlash(rel), name)] = true
		}
	}

	return built, nil
}

// location is one place gopls reported: a file of the module, a line, and
// a byte column, the last two counted from one.
type location struct {
	file      string
	line, col int
}

func (l location) String() string {
	return fmt.Sprintf("%s:%d:%d", filepath.FromSlash(l.file), l.line, l.col)
}

// offset is where the location sits in src when word is there, and false
// when something else is.
func (l location) offset(src []byte, word string) (int, bool) {
	at := 0

	for range l.line - 1 {
		i := bytes.IndexByte(src[at:], '\n')
		if i < 0 {
			return 0, false
		}

		at += i + 1
	}

	at += l.col - 1
	if l.line < 1 || l.col < 1 || at > len(src) || !bytes.HasPrefix(src[at:], []byte(word)) {
		return 0, false
	}

	return at, true
}

// locationFields is what locationPattern yields: the match, then the file,
// line, and column.
const locationFields = 4

var locationPattern = regexp.MustCompile(`^(.+\.go):(\d+):(\d+)`)

// locations reads the file:line:col lines of gopls output, leaving out
// places outside the module.
func (t toolchain) locations(out string) []location {
	var locs []location

	for line := range strings.SplitSeq(t.relativize(out), "\n") {
		m := locationPattern.FindStringSubmatch(strings.TrimSpace(line))
		if len(m) != locationFields || filepath.IsAbs(m[1]) {
			continue
		}

		row, rowErr := strconv.Atoi(m[2])
		col, colErr := strconv.Atoi(m[3])

		if rowErr == nil && colErr == nil {
			locs = append(locs, location{file: filepath.ToSlash(m[1]), line: row, col: col})
		}
	}

	return locs
}

// references lists every use of the identifier at a position, and with
// declared set its declaration too.
func (t toolchain) references(ctx context.Context, at string, declared bool) ([]location, error) {
	args := []string{"references"}
	if declared {
		args = append(args, "-d")
	}

	out, err := t.runGopls(ctx, "reference search", append(args, at)...)
	if err != nil {
		return nil, err
	}

	return t.locations(out), nil
}

// fixImports has gopls drop the imports each file no longer uses and add
// the ones it can resolve, one process per file, a few at a time.
func (t toolchain) fixImports(ctx context.Context, files []string) error {
	return inParallel(len(files), func(i int) error {
		_, err := t.runGopls(ctx, "import fix-up", "imports", "-w", filepath.FromSlash(files[i]))

		return err
	})
}

// inParallel calls work for each index below n, at most maxParallel at a
// time, and returns the first failure by index.
func inParallel(n int, work func(i int) error) error {
	var (
		wg    sync.WaitGroup
		errs  = make([]error, n)
		slots = make(chan struct{}, maxParallel)
	)

	for i := range n {
		wg.Go(func() {
			slots <- struct{}{}

			defer func() { <-slots }()

			errs[i] = work(i)
		})
	}

	wg.Wait()

	for _, err := range errs {
		if err != nil {
			return err
		}
	}

	return nil
}
