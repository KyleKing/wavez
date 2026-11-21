package refactor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
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

// rename asks gopls to rename the identifier at an offset everywhere it is
// used, writing the files, and returns the files it changed.
func (t toolchain) rename(ctx context.Context, rel string, offset int, to string) ([]string, error) {
	out, err := t.runGopls(ctx, "rename", "rename", "-l", "-w", position(rel, offset), to)
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

// location is one place gopls reported.
type location struct {
	file string
	line int
}

var locationPattern = regexp.MustCompile(`^(.+\.go):(\d+):`)

// references lists every use of the identifier at an offset, without the
// declaration itself.
func (t toolchain) references(ctx context.Context, rel string, offset int) ([]location, error) {
	out, err := t.runGopls(ctx, "reference search", "references", position(rel, offset))
	if err != nil {
		return nil, err
	}

	var locs []location

	for line := range strings.SplitSeq(t.relativize(out), "\n") {
		m := locationPattern.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}

		n, convErr := strconv.Atoi(m[2])
		if convErr != nil {
			continue
		}

		locs = append(locs, location{file: filepath.ToSlash(m[1]), line: n})
	}

	return locs, nil
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
