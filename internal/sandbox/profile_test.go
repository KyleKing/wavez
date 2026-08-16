package sandbox_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/sandbox"
)

var update = flag.Bool("update", false, "update golden files")

func TestRenderProfile_Golden(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		projectRoot string
		sessionTmp  string
		home        string
		golden      string
	}{
		{
			name:        "loopback network scope",
			projectRoot: "/PROJECT_ROOT",
			sessionTmp:  "/SESSION_TMP",
			home:        "/HOME",
			golden:      "testdata/profile.golden",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := sandbox.RenderProfile(tt.projectRoot, tt.sessionTmp, tt.home)

			if *update {
				if err := os.WriteFile(tt.golden, []byte(got), 0o600); err != nil {
					t.Fatalf("writing golden file: %v", err)
				}
			}

			want, err := os.ReadFile(tt.golden)
			if err != nil {
				t.Fatalf("reading golden file: %v", err)
			}
			if got != string(want) {
				t.Errorf("RenderProfile() mismatch\ngot:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

func TestRenderProfile_QuotesSecretPaths(t *testing.T) {
	t.Parallel()

	got := sandbox.RenderProfile("/root", "/tmp/session", "/Users/kyle")

	for _, want := range []string{
		`(subpath "/Users/kyle/.ssh")`,
		`(subpath "/Users/kyle/.aws")`,
		`(subpath "/Users/kyle/.config/gh")`,
		`(subpath "/Users/kyle/Library/Keychains")`,
		`(subpath "/Users/kyle/.claude")`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("RenderProfile() missing deny fragment %q", want)
		}
	}
}

func TestNewProfile_ResolvesSymlinks(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	realDir := filepath.Join(dir, "real")
	if err := os.Mkdir(realDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(realDir, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	sessionTmp := t.TempDir()
	profile, err := sandbox.NewProfile(link, sessionTmp)
	if err != nil {
		t.Fatalf("NewProfile: %v", err)
	}

	resolvedReal, err := filepath.EvalSymlinks(realDir)
	if err != nil {
		t.Fatalf("resolving real dir: %v", err)
	}
	if !strings.Contains(profile.Render(), sbSubpath(resolvedReal)) {
		t.Errorf("Render() does not reference resolved path %q:\n%s", resolvedReal, profile.Render())
	}
	if strings.Contains(profile.Render(), sbSubpath(link)) {
		t.Errorf("Render() references unresolved symlink %q:\n%s", link, profile.Render())
	}
}

func TestNewProfile_MissingDirFails(t *testing.T) {
	t.Parallel()

	_, err := sandbox.NewProfile(filepath.Join(t.TempDir(), "does-not-exist"), t.TempDir())
	if err == nil {
		t.Fatal("NewProfile() with a missing project root: want error, got nil")
	}
}

func sbSubpath(path string) string {
	return `(subpath "` + path + `")`
}
