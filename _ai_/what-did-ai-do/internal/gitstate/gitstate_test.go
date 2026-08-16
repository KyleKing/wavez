package gitstate_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/what-did-ai-do/internal/gitstate"
)

func TestResolve_FileExists(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		fileContent  string
		expectedText string
		wantStatus   gitstate.Status
	}{
		{
			name:         "expected text present verbatim",
			fileContent:  "func Foo() {\n\treturn 1\n}\n",
			expectedText: "func Foo() {\n\treturn 1\n}\n",
			wantStatus:   gitstate.StatusLive,
		},
		{
			name:         "expected text present with different trailing whitespace and blank lines",
			fileContent:  "func Foo() {   \n\treturn 1   \n\n\n\n}\n",
			expectedText: "func Foo() {\n\treturn 1\n\n}\n",
			wantStatus:   gitstate.StatusLive,
		},
		{
			name:         "expected text absent, file rewritten",
			fileContent:  "func Bar() {\n\treturn 2\n}\n",
			expectedText: "func Foo() {\n\treturn 1\n}\n",
			wantStatus:   gitstate.StatusSuperseded,
		},
		{
			name:         "empty expected text treated as live",
			fileContent:  "anything at all",
			expectedText: "",
			wantStatus:   gitstate.StatusLive,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			path := "main.go"
			if err := os.WriteFile(
				filepath.Join(dir, path),
				[]byte(tt.fileContent),
				0o600,
			); err != nil {
				t.Fatalf("writing fixture file: %v", err)
			}

			state, err := gitstate.Resolve(context.Background(), dir, path, tt.expectedText)
			if err != nil {
				t.Fatalf("Resolve() unexpected error: %v", err)
			}
			if state.Status != tt.wantStatus {
				t.Errorf("Status = %q; want %q", state.Status, tt.wantStatus)
			}
			if state.Status == gitstate.StatusSuperseded &&
				!strings.Contains(state.CurrentText, "Bar") {
				t.Errorf("CurrentText = %q; want it to reflect new content", state.CurrentText)
			}
		})
	}
}

func TestResolve_FileGoneNoGitRepo(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	state, err := gitstate.Resolve(context.Background(), dir, "missing.go", "some text")
	if err != nil {
		t.Fatalf("Resolve() unexpected error: %v", err)
	}
	if state.Status != gitstate.StatusGone {
		t.Errorf("Status = %q; want %q", state.Status, gitstate.StatusGone)
	}
	if state.RenamedTo != "" {
		t.Errorf("RenamedTo = %q; want empty", state.RenamedTo)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
}

func TestResolve_FileGoneInGitRepoRenameDetection(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available in test environment")
	}

	dir := t.TempDir()
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "test")
	runGit(t, dir, "config", "commit.gpgsign", "false")

	oldPath := "old_name.go"
	newPath := "new_name.go"
	content := "package foo\n\nfunc Foo() int {\n\treturn 1\n}\n"
	if err := os.WriteFile(filepath.Join(dir, oldPath), []byte(content), 0o600); err != nil {
		t.Fatalf("writing fixture file: %v", err)
	}
	runGit(t, dir, "add", oldPath)
	runGit(t, dir, "commit", "-m", "add old_name.go")

	runGit(t, dir, "mv", oldPath, newPath)
	runGit(t, dir, "commit", "-m", "rename to new_name.go")

	state, err := gitstate.Resolve(context.Background(), dir, oldPath, content)
	if err != nil {
		t.Fatalf("Resolve() unexpected error: %v", err)
	}
	if state.Status != gitstate.StatusGone {
		t.Errorf("Status = %q; want %q", state.Status, gitstate.StatusGone)
	}

	if state.RenamedTo != newPath {
		t.Errorf("RenamedTo = %q; want %q", state.RenamedTo, newPath)
	}
}

func TestResolve_ProjectPathDoesNotExist(t *testing.T) {
	t.Parallel()

	_, err := gitstate.Resolve(
		context.Background(),
		"/nonexistent/path/for/gitstate/test",
		"file.go",
		"text",
	)
	if err == nil {
		t.Fatal("Resolve() expected error for nonexistent projectPath, got nil")
	}
}

func TestResolve_PathTraversalEscapesProject(t *testing.T) {
	t.Parallel()

	outsideDir := t.TempDir()
	secretPath := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(secretPath, []byte("top secret contents"), 0o600); err != nil {
		t.Fatalf("writing secret fixture: %v", err)
	}

	projectDir := t.TempDir()

	traversal, err := filepath.Rel(projectDir, secretPath)
	if err != nil {
		t.Fatalf("computing traversal path: %v", err)
	}

	state, err := gitstate.Resolve(
		context.Background(),
		projectDir,
		traversal,
		"top secret contents",
	)
	if err != nil {
		t.Fatalf("Resolve() unexpected error: %v", err)
	}
	if state.Status != gitstate.StatusUnknown {
		t.Errorf("Status = %q; want %q", state.Status, gitstate.StatusUnknown)
	}
	if strings.Contains(state.CurrentText, "top secret") {
		t.Errorf("CurrentText leaked content from outside projectPath: %q", state.CurrentText)
	}
}

func TestResolve_CurrentTextTruncation(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := "big.go"
	big := strings.Repeat("a", 10_000)
	if err := os.WriteFile(filepath.Join(dir, path), []byte(big), 0o600); err != nil {
		t.Fatalf("writing fixture file: %v", err)
	}

	state, err := gitstate.Resolve(context.Background(), dir, path, "")
	if err != nil {
		t.Fatalf("Resolve() unexpected error: %v", err)
	}
	if len(state.CurrentText) > 4000 {
		t.Errorf("CurrentText length = %d; want <= 4000", len(state.CurrentText))
	}
}
