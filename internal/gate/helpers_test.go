package gate_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/gate"
)

// goMod declares a toolchain old enough for whatever golangci-lint the
// machine has to accept.
const goMod = "module example.com/scratch\n\ngo 1.24\n"

var errNotInstalled = errors.New("not installed")

// project writes files under a fresh root and returns it.
func project(t *testing.T, files map[string]string) string {
	t.Helper()

	root := t.TempDir()
	for name, body := range files {
		writeFile(t, root, name, body)
	}

	return root
}

func writeFile(t *testing.T, root, name, body string) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("creating %s: %v", filepath.Dir(path), err)
	}

	//nolint:gosec // fixtures include scripts, which must be executable
	if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
		t.Fatalf("writing %s: %v", name, err)
	}
}

func readFile(t *testing.T, root, name string) string {
	t.Helper()

	data, err := fs.ReadFile(os.DirFS(root), name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}

	return string(data)
}

// without returns a lookup that cannot find the named tools and defers to
// the real PATH for the rest.
func without(tools ...string) gate.Option {
	return gate.WithLookPath(func(name string) (string, error) {
		for _, tool := range tools {
			if tool == name {
				return "", errNotInstalled
			}
		}

		return exec.LookPath(name)
	})
}

// replacing returns a lookup that resolves tool to path.
func replacing(tool, path string) gate.Option {
	return gate.WithLookPath(func(name string) (string, error) {
		if name == tool {
			return path, nil
		}

		return exec.LookPath(name)
	})
}

func runGate(
	ctx context.Context, t *testing.T, root string, stage gate.Stage, changed []string, opts ...gate.Option,
) gate.Report {
	t.Helper()

	report, err := gate.New(opts...).Run(ctx, root, changed, stage)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	return report
}

// want is what a test expects of one result. Summary is a substring.
type want struct {
	check   string
	status  gate.Status
	summary string
}

// expect fails unless the report holds exactly the wanted results in order.
func expect(t *testing.T, report gate.Report, wants ...want) {
	t.Helper()

	if len(report.Results) != len(wants) {
		t.Fatalf("got %d results, want %d: %+v", len(report.Results), len(wants), report.Results)
	}

	for i, w := range wants {
		got := report.Results[i]
		if got.Check != w.check || got.Status != w.status || !strings.Contains(got.Summary, w.summary) {
			t.Errorf("result %d = %+v, want %+v", i, got, w)
		}

		if got.Status == gate.StatusPass && got.Summary != "" {
			t.Errorf("result %d passed with a summary: %q", i, got.Summary)
		}
	}
}
