package gate_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/gate"
)

func TestBuildAhead_CompilesAPackageOfAModuleBelowTheRoot(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("NOT TESTED: go is not on PATH")
	}

	root, cache := t.TempDir(), t.TempDir()
	files := map[string]string{
		"svc/go.mod":       "module example.com/svc\n\ngo 1.21\n",
		"svc/a/a.go":       "package a\n\nfunc A() int { return 1 }\n",
		"svc/a/a_test.go":  "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) { t.Fatal(\"never run\") }\n",
		"svc/a/written.go": "package a\n",
	}

	for name, body := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o750))
		require.NoError(t, os.WriteFile(full, []byte(body), 0o600))
	}

	// The cache is this test's own, so what the build left is countable.
	t.Setenv("GOCACHE", cache)
	t.Setenv("GOFLAGS", "-mod=mod")

	gate.BuildAhead(t.Context(), root, []string{"./svc/a/", "./nowhere/", "../outside"})

	entries, err := os.ReadDir(cache)
	require.NoError(t, err)
	assert.NotEmpty(t, entries, "the build wrote to the cache")

	left, err := os.ReadDir(filepath.Join(root, "svc", "a"))
	require.NoError(t, err)
	assert.Len(t, left, 3, "nothing is written into the tree, and no test ran")
}
