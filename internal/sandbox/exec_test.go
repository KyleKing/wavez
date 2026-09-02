package sandbox_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/sandbox"
)

func requireSandboxExec(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sandbox-exec"); err != nil {
		t.Skip("sandbox-exec not available on this host")
	}
}

func TestExec_Probes(t *testing.T) {
	requireSandboxExec(t)
	t.Parallel()

	projectRoot := t.TempDir()
	sessionTmp := t.TempDir()
	outsideDir := t.TempDir()

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("os.UserHomeDir: %v", err)
	}
	sshConfig := filepath.Join(home, ".ssh", "config")
	if _, err := os.Stat(sshConfig); err != nil {
		t.Skipf("no %s to probe against: %v", sshConfig, err)
	}

	tests := []struct {
		name   string
		args   []string
		wantOK bool
	}{
		{
			name:   "write inside project root",
			args:   []string{"sh", "-c", "echo x > '" + filepath.Join(projectRoot, "probe") + "'"},
			wantOK: true,
		},
		{
			name:   "write outside project root",
			args:   []string{"sh", "-c", "echo x > '" + filepath.Join(outsideDir, "probe") + "'"},
			wantOK: false,
		},
		{
			name:   "read ~/.ssh",
			args:   []string{"cat", sshConfig},
			wantOK: false,
		},
		{
			// uv exits before it runs anything when it cannot open its
			// cache, and the machine's own cache is readable and not
			// writable here, so the redirect is what makes a Python
			// project's runner work at all.
			name:   "write the redirected user cache",
			args:   []string{"sh", "-c", "echo x > \"$UV_CACHE_DIR/probe\" && echo x > \"$XDG_CACHE_HOME/probe\""},
			wantOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := sandbox.Exec(context.Background(), projectRoot, sessionTmp, tt.args...)
			if err != nil {
				t.Fatalf("Exec: %v", err)
			}

			gotOK := result.ExitCode == 0
			if gotOK != tt.wantOK {
				t.Errorf("Exec(%v) exit=%d stderr=%q, want ok=%v", tt.args, result.ExitCode, result.Stderr, tt.wantOK)
			}
		})
	}
}

func TestExec_NoCommand(t *testing.T) {
	t.Parallel()

	_, err := sandbox.Exec(context.Background(), t.TempDir(), t.TempDir())
	if err == nil {
		t.Fatal("Exec() with no args: want error, got nil")
	}
}

// A sandboxed build resolves this module's own dependencies. The module
// cache is on the machine and the profile denies network, so a build that
// cannot read that cache reports a DNS failure against proxy.golang.org
// instead of compiling, which is what made every gate and every agent-run
// `go test` fail from inside the sandbox.
func TestExec_GoBuildResolvesDependenciesWithoutNetwork(t *testing.T) {
	requireSandboxExec(t)
	t.Parallel()

	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("no go toolchain on this host")
	}

	root, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	root = filepath.Dir(filepath.Dir(root))

	result, err := sandbox.Exec(context.Background(), root, t.TempDir(),
		"go", "build", "-o", os.DevNull, "./internal/tui")
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("go build exit=%d, want 0\nstderr: %s", result.ExitCode, result.Stderr)
	}
}

// TestExecDropsSecretNamedEnv pins the leak the sandbox's network deny does
// not cover: a command's stdout enters the thread's context, and the next
// hosted turn sends that context to the provider. The API key the daemon
// falls back to lives in this environment, and `echo` is a command the guard
// allows by name.
func TestExecDropsSecretNamedEnv(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "sk-should-not-appear")
	t.Setenv("WAVEZ_PROBE_PLAIN", "visible")

	dir := t.TempDir()

	result, err := sandbox.Exec(context.Background(), dir, dir,
		"sh", "-c", `echo "key=[$OPENROUTER_API_KEY] plain=[$WAVEZ_PROBE_PLAIN]"`)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}

	if strings.Contains(result.Stdout, "sk-should-not-appear") {
		t.Errorf("secret reached the command: %q", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "plain=[visible]") {
		t.Errorf("an ordinary variable was dropped too: %q", result.Stdout)
	}
}
