package gate_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/gate"
)

// fakeDetest puts a detest on PATH that prints stdout and exits with status.
func fakeDetest(t *testing.T, stdout, status string) {
	t.Helper()

	bin := t.TempDir()
	script := "#!/bin/sh\ncat <<'EOF'\n" + stdout + "\nEOF\nexit " + status + "\n"
	//nolint:gosec // a script the test runs
	require.NoError(t, os.WriteFile(filepath.Join(bin, "detest"), []byte(script), 0o700))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

//nolint:paralleltest // each case puts its own detest on PATH
func TestDetest_Select(t *testing.T) {
	const picked = `{"status":"selected","picks":[
		{"id":"internal/a::TestOne"},{"id":"internal/a::TestTable/case_two"},
		{"id":".::TestRoot"},{"id":"tests/test_x.py::test_y"}],
		"test_files":["internal/b/b_test.go","internal/gone/gone_test.go","tests/test_z.py"]}`

	tests := []struct {
		name   string
		stdout string
		status string
		base   string
		want   gate.Selection
		errs   bool
	}{
		{
			name: "picks and whole test files become tests by package", stdout: picked, status: "0", base: "abc",
			want: gate.Selection{Complete: true, Tests: map[string][]string{
				"./internal/a": {"TestOne", "TestTable"},
				"./":           {"TestRoot"},
				"./internal/b": {"TestB", "TestOther"},
			}},
		},
		{
			name:   "a selection detest cannot trust runs everything",
			stdout: `{"status":"abstain"}`, status: "3", base: "abc",
		},
		{name: "a detest that failed is an error, which runs everything", status: "2", base: "abc", errs: true},
		{name: "an answer that is not JSON is an error", stdout: "nope", status: "0", base: "abc", errs: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(root, ".detest"), 0o750))
			require.NoError(t, os.MkdirAll(filepath.Join(root, "internal", "b"), 0o750))
			src := "package b\n\nfunc TestB(t *testing.T) {}\n\nfunc helper() {}\n\nfunc TestOther(t *testing.T) {}\n"
			require.NoError(t, os.WriteFile(filepath.Join(root, "internal", "b", "b_test.go"), []byte(src), 0o600))
			fakeDetest(t, tt.stdout, tt.status)

			selector, ok := gate.Detest(root)
			require.True(t, ok)

			got, err := selector.Select(t.Context(), root, tt.base, nil)
			if tt.errs {
				require.Error(t, err)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDetest_OnlyWhereTheProjectUsesIt(t *testing.T) {
	root := t.TempDir()

	fakeDetest(t, "{}", "0")

	_, ok := gate.Detest(root)
	assert.False(t, ok, "a project with no .detest directory does not use detest")

	require.NoError(t, os.MkdirAll(filepath.Join(root, ".detest"), 0o750))

	_, ok = gate.Detest(root)
	assert.True(t, ok)

	t.Setenv("PATH", t.TempDir())

	_, ok = gate.Detest(root)
	assert.False(t, ok, "and neither does a machine without the binary")
}
