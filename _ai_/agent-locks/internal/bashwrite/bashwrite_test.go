package bashwrite

import "testing"

func TestDetectWrites(t *testing.T) {
	cases := []struct {
		name    string
		command string
		want    bool
		path    string
	}{
		{"redirect", "echo hi > notes/out.txt", true, "notes/out.txt"},
		{"append", "cat a >> build/log.txt", true, "build/log.txt"},
		{"tee", "echo x | tee -a internal/api/gen.go", true, "internal/api/gen.go"},
		{"sed in place", "sed -i '' 's/a/b/' cmd/main.go", true, "cmd/main.go"},
		{"gofmt", "gofmt -w internal/store/store.go", true, "internal/store/store.go"},
		{"go generate", "go generate ./...", true, ""},
		{"move", "mv old/name.go new/name.go", true, "new/name.go"},
		{"plain read", "cat internal/api/routes.go", false, ""},
		{"grep", "grep -rn foo internal/", false, ""},
		{"git status", "git status --porcelain", false, ""},
		{"stderr redirect", "go build ./... 2>&1", false, ""},
		{"dev null", "go test ./... > /dev/null", false, ""},
		{"comparison", "test 3 > 2 && echo yes", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Detect(tc.command)
			if got.IsWrite != tc.want {
				t.Fatalf("IsWrite = %v, want %v (reason %q, paths %v)", got.IsWrite, tc.want, got.Reason, got.Paths)
			}
			if tc.path != "" && !contains(got.Paths, tc.path) {
				t.Fatalf("paths = %v, want to contain %q", got.Paths, tc.path)
			}
		})
	}
}

// Commit messages carry trailers and prose that used to be mistaken for redirect
// targets, which attributed subtrees to sessions that never wrote there.
func TestCommitMessageIsNotAWrite(t *testing.T) {
	msg := `git commit -m "feat(api): add endpoint

Claude-Session: https://claude.ai/code/session_01
Co-Authored-By: Someone <x@example.com>"`
	got := Detect(msg)
	for _, p := range got.Paths {
		if p == "Claude-Session:" || p == "https://claude.ai/code/session_01" {
			t.Fatalf("extracted %q from a commit message", p)
		}
	}
}

func TestIsGitCommit(t *testing.T) {
	for _, c := range []string{"git commit -m x", "git -C /tmp/x commit --amend"} {
		if !IsGitCommit(c) {
			t.Errorf("IsGitCommit(%q) = false", c)
		}
	}
	for _, c := range []string{"git status", "git log --oneline", "git -C /tmp/x status"} {
		if IsGitCommit(c) {
			t.Errorf("IsGitCommit(%q) = true", c)
		}
	}
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle {
			return true
		}
	}
	return false
}
