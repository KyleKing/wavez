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
		policy      sandbox.Policy
	}{
		{
			name:        "no policy",
			projectRoot: "/PROJECT_ROOT",
			sessionTmp:  "/SESSION_TMP",
			home:        "/HOME",
			golden:      "testdata/profile.golden",
		},
		{
			name:        "extra read dirs and one model port",
			projectRoot: "/HOME/work/proj",
			sessionTmp:  "/SESSION_TMP",
			home:        "/HOME",
			policy:      sandbox.Policy{ReadDirs: []string{"/HOME/work/sibling"}, LoopbackPorts: []int{8080}},
			golden:      "testdata/profile_policy.golden",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := sandbox.RenderProfile(tt.projectRoot, tt.sessionTmp, tt.home, tt.policy)

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

// The two guarantees the string-matching guard cannot make. A run reaches an
// interpreter through a dozen spellings the guard never sees, and a leak
// leaves through the transcript rather than through a socket, so both the
// protected-path deny and the read inversion have to be rules the kernel
// checks rather than command lines a regex reads.
func TestRenderProfile_DeniesWhatTheGuardCannot(t *testing.T) {
	t.Parallel()

	got := sandbox.RenderProfile("/Users/kyle/work/proj", "/tmp/session", "/Users/kyle", sandbox.Policy{})

	for _, want := range []string{
		`(deny file-read* (subpath "/Users/kyle"))`,
		`(subpath "/Users/kyle/work/proj/.wavez.pkl")`,
		`(subpath "/Users/kyle/work/proj/hk.pkl")`,
		`(regex #"^/Users/kyle/work/proj(/.*)?/\.git($|/)")`,
		`(literal "/Users/kyle/work")`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("RenderProfile() missing fragment %q", want)
		}
	}

	if strings.Contains(got, `/Users/kyle/.ssh`) {
		t.Error("RenderProfile() still names a credential path, so reads are back to a denylist")
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
	profile, err := sandbox.NewProfile(link, sessionTmp, sandbox.Policy{})
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

	_, err := sandbox.NewProfile(filepath.Join(t.TempDir(), "does-not-exist"), t.TempDir(), sandbox.Policy{})
	if err == nil {
		t.Fatal("NewProfile() with a missing project root: want error, got nil")
	}
}

func sbSubpath(path string) string {
	return `(subpath "` + path + `")`
}
