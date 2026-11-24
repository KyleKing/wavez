package refactor_test

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/refactor"
)

const goMod = "module example.com/m\n\ngo 1.24\n"

const libSource = `// Package lib is the fixture the refactors run against.
package lib

import (
	"fmt"
	"strings"
)

// Greet greets name. Greet is called from two packages.
func Greet(name string) string {
	return "hello " + shout(name)
}

// shout upper-cases s.
func shout(s string) string { return strings.ToUpper(s) } // loud

// Taken exists so a rename can collide with it.
func Taken() {}

// Box holds a size.
type Box struct {
	Size int
}

// Grow makes the box larger.
func (b *Box) Grow() { b.Size++ }

// String implements fmt.Stringer.
func (b Box) String() string { return fmt.Sprint(b.Size) }

var _ fmt.Stringer = Box{}

// Lid closes a box.
type Lid struct{}

// Run is declared on two types.
func (Lid) Run() {}

// Run is declared on two types.
func (*Box) Run() {}

// Limits bound a box.
const (
	MinSize = 1
	MaxSize = 9
)

// unused is referenced by nothing.
func unused() string { return fmt.Sprint(MinSize, MaxSize) }
`

const libTestSource = `package lib_test

import (
	"testing"

	"example.com/m/lib"
)

// TestGreet covers Greet.
func TestGreet(t *testing.T) {
	if lib.Greet("x") != "hello X" {
		t.Fatal("Greet returned the wrong greeting")
	}
}

func TestBox(t *testing.T) {
	b := lib.Box{}
	b.Grow()

	if b.Size != 1 {
		t.Fatal("Grow did nothing")
	}
}
`

const appSource = `// Package app uses lib.
package app

import "example.com/m/lib"

// Hello calls lib.Greet, not Greeting.
func Hello() string { return lib.Greet("app") }
`

// fixture writes the standard module into a temp directory and returns its
// root. Extra files are written over it.
func fixture(t *testing.T, extra map[string]string) string {
	t.Helper()

	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	files := map[string]string{
		"go.mod":          goMod,
		"lib/lib.go":      libSource,
		"lib/lib_test.go": libTestSource,
		"app/app.go":      appSource,
	}
	for name, content := range extra {
		files[name] = content
	}

	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}

	return root
}

// needGopls skips a test on a machine without gopls, since these tests run
// the real one.
func needGopls(t *testing.T) {
	t.Helper()

	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls is not on PATH")
	}
}

func newRefactorer(t *testing.T, root string, opts ...refactor.Option) *refactor.Refactorer {
	t.Helper()

	r, err := refactor.New(root, opts...)
	require.NoError(t, err)

	return r
}

func read(t *testing.T, root, rel string) string {
	t.Helper()

	data, err := fs.ReadFile(os.DirFS(root), rel)
	require.NoError(t, err)

	return string(data)
}

// contents reads every file under root, which is how a test proves a
// refused operation left the tree alone.
func contents(t *testing.T, root string) map[string]string {
	t.Helper()

	out := map[string]string{}
	fsys := os.DirFS(root)

	require.NoError(t, fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		data, readErr := fs.ReadFile(fsys, path)
		require.NoError(t, readErr)

		out[path] = string(data)

		return nil
	}))

	return out
}

// refused asserts an operation failed the way a case expects and left
// every file as it was before.
func refused(
	t *testing.T, root string, before map[string]string, err error, check func(*testing.T, error), msg string,
) {
	t.Helper()

	require.Error(t, err)
	check(t, err)
	assert.Contains(t, err.Error(), msg)
	assert.Equal(t, before, contents(t, root), "a refused operation must leave the tree alone")
}

// builds asserts the module compiles, tests included.
func builds(t *testing.T, root string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "go", "vet", "./...")
	cmd.Dir = root

	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}
