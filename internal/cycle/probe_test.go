package cycle_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/cycle"
	"github.com/kyleking/wavez/internal/tool"
)

// The prober is what turns "there is a failing test" into a reading. It has
// to see a real failure and a real pass, and a test the change set does not
// declare must not be observed at all.
func TestGoProber_ObservesTheChangeSetsTests(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(body), 0o600))
	}

	write("go.mod", "module probe\n\ngo 1.25\n")
	write("lease.go", "package lease\n\nfunc TTL() int { return 0 }\n")
	write("lease_test.go", `package lease

import "testing"

func TestTTL(t *testing.T) {
	if TTL() != 5 {
		t.Fatalf("want 5, got %d", TTL())
	}
}

func TestUntouched(t *testing.T) { t.Log("never selected") }
`)

	changes := []tool.Change{{Path: "lease_test.go", Ranges: []tool.LineRange{{Start: 5, End: 9}}}}

	observed, err := cycle.NewGoProber().Probe(t.Context(), root, changes)
	require.NoError(t, err)
	require.Len(t, observed, 1)
	assert.Equal(t, "TestTTL", observed[0].Test)
	assert.True(t, observed[0].Failed)
	assert.Contains(t, observed[0].Detail, "want 5, got 0")

	write("lease.go", "package lease\n\nfunc TTL() int { return 5 }\n")

	observed, err = cycle.NewGoProber().Probe(t.Context(), root, changes)
	require.NoError(t, err)
	require.Len(t, observed, 1)
	assert.False(t, observed[0].Failed)
}
