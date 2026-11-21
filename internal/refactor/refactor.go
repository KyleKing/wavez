// Package refactor implements the edits that take a name instead of text:
// rename, move, and delete for Go declarations.
//
// Declarations are located with go/parser, and everything that needs type
// information is asked of the gopls command line, one process per question.
// Every operation is atomic in effect: it keeps the bytes of each file before
// touching it, and puts them back when a later step or the closing build
// check fails.
package refactor

import (
	"fmt"
	"os"
	"path/filepath"
)

// Refactorer runs refactors against one Go module.
type Refactorer struct {
	root    string
	workDir string
	gopls   string
	goTool  string
}

// Option adjusts a Refactorer.
type Option func(*Refactorer)

// WithGopls names the gopls program to run, for a toolchain that is not on
// PATH under its usual name.
func WithGopls(program string) Option {
	return func(r *Refactorer) { r.gopls = program }
}

// WithWorkDir sets the directory a relative target path is tried against
// before the module root, which is what makes a path typed from a
// subdirectory mean what the caller sees there.
func WithWorkDir(dir string) Option {
	return func(r *Refactorer) { r.workDir = dir }
}

// New builds a Refactorer for the module rooted at root. The root is resolved
// through symlinks so the paths gopls prints can be made relative to it.
func New(root string, opts ...Option) (*Refactorer, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolving root %s: %w", root, err)
	}

	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("resolving root %s: %w", root, err)
	}

	r := &Refactorer{root: resolved, workDir: resolved, gopls: "gopls", goTool: "go"}
	for _, opt := range opts {
		opt(r)
	}

	return r, nil
}

// FindRoot walks upward from dir to the nearest directory holding a go.mod.
// It stops when a directory is its own parent, which is the filesystem root
// and also what filepath.Dir(".") returns forever.
func FindRoot(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolving %s: %w", dir, err)
	}

	for cur := abs; ; {
		if info, statErr := os.Stat(filepath.Join(cur, "go.mod")); statErr == nil && !info.IsDir() {
			return cur, nil
		}

		parent := filepath.Dir(cur)
		if parent == cur {
			return "", &RootError{Dir: abs}
		}

		cur = parent
	}
}

// session is the state of one operation: the files it has parsed and the
// bytes it must put back if the operation fails.
type session struct {
	tree  *tree
	snap  *snapshot
	tools toolchain
	work  string
}

func (r *Refactorer) open() (*session, error) {
	fsys, err := os.OpenRoot(r.root)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", r.root, err)
	}

	return &session{
		tree:  &tree{fsys: fsys, dir: r.root, cache: map[string]*parsedFile{}},
		snap:  &snapshot{fsys: fsys, kept: map[string]savedFile{}},
		tools: toolchain{dir: r.root, gopls: r.gopls, goTool: r.goTool},
		work:  r.workDir,
	}, nil
}

// close releases the root handle. Its error is dropped because every write
// has already been checked and nothing is buffered behind the handle.
func (s *session) close() {
	_ = s.tree.fsys.Close() //nolint:errcheck // a directory handle with nothing buffered
}
