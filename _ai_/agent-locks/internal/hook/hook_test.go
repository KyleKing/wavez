package hook

import (
	"os"
	"path/filepath"
	"testing"
)

// The same directory reached through a symlink must produce one key. macOS reports
// /var and /private/var for the same path depending on who resolved it, which silently
// split leases into two non-matching sets.
func TestResolveCollapsesSymlinks(t *testing.T) {
	real := t.TempDir()
	repo := filepath.Join(real, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "internal", "api"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	direct := resolve(filepath.Join(repo, "internal", "api", "routes.go"), repo)
	viaLink := resolve(filepath.Join(link, "internal", "api", "routes.go"), link)

	if direct.Root != viaLink.Root {
		t.Errorf("roots differ:\n  direct %s\n  link   %s", direct.Root, viaLink.Root)
	}
	if direct.Dir != viaLink.Dir {
		t.Errorf("dirs differ: %q vs %q", direct.Dir, viaLink.Dir)
	}
	if direct.Dir != filepath.Join("internal", "api") {
		t.Errorf("dir = %q, want internal/api", direct.Dir)
	}
}

func TestResolveRelativePathUsesCWD(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "cmd"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := resolve("cmd/main.go", repo)
	if got.Dir != "cmd" {
		t.Errorf("dir = %q, want cmd", got.Dir)
	}
}

func TestTargetsFromBashFormatter(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "internal", "store"), 0o755); err != nil {
		t.Fatal(err)
	}
	in := &Input{ToolName: "Bash", CWD: repo}
	in.ToolInput.Command = "gofmt -w internal/store/store.go"
	got := in.Targets()
	if len(got) != 1 || got[0].Dir != filepath.Join("internal", "store") {
		t.Fatalf("targets = %+v, want one target on internal/store", got)
	}
}
