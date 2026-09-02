package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrNoCommand reports Exec called with no command to run.
var ErrNoCommand = errors.New("sandbox: no command given")

const sessionDirPerm = 0o700

// Result is the outcome of a command run under Exec.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Exec runs args under a Seatbelt profile scoped to projectRoot and
// sessionTmp, with the caches a build writes to (GOCACHE,
// GOLANGCI_LINT_CACHE, GOTMPDIR, and TMPDIR, which is what cgo's compiler
// driver uses, plus XDG_CACHE_HOME and UV_CACHE_DIR, which is what uv
// refuses to start without) redirected into sessionTmp. It fails closed: any error resolving paths or building the
// profile aborts before sandbox-exec is invoked, and a nonzero exit from
// the sandboxed command is reported in Result rather than as an error.
//
// The module cache is deliberately not redirected. A fresh one would be
// empty, and the profile denies network, so every build of a package with
// an external dependency would fail on a DNS lookup of proxy.golang.org.
// The machine's own cache is readable under the profile and unwritable
// outside it, which is the property that lets a build read what is already
// there without a sandboxed command poisoning it for the rest of the
// machine. GOPROXY=off makes a module that is genuinely missing say so
// rather than reporting it as a network failure.
func Exec(ctx context.Context, projectRoot, sessionTmp string, args ...string) (Result, error) {
	cmd, done, err := Command(ctx, projectRoot, sessionTmp, args...)
	if err != nil {
		return Result{}, err
	}
	defer done()

	var stdout, stderr bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	result := Result{Stdout: stdout.String(), Stderr: stderr.String()}

	var exitErr *exec.ExitError

	switch {
	case runErr == nil:
		return result, nil
	case errors.As(runErr, &exitErr):
		result.ExitCode = exitErr.ExitCode()

		return result, nil
	default:
		return Result{}, fmt.Errorf("running sandboxed command: %w", runErr)
	}
}

// Command prepares args to run under the same profile Exec uses, without
// running it, for a caller that owns the process's input and output: driving
// a program under a pseudo-terminal is one, and a terminal is not something
// a buffer can stand in for.
//
// The returned function removes the profile file and must be called once the
// command has finished, which is why the command is not started here.
func Command(
	ctx context.Context, projectRoot, sessionTmp string, args ...string,
) (*exec.Cmd, func(), error) {
	if len(args) == 0 {
		return nil, nil, ErrNoCommand
	}

	profile, err := NewProfile(projectRoot, sessionTmp)
	if err != nil {
		return nil, nil, fmt.Errorf("building sandbox profile: %w", err)
	}

	dirs, err := prepareCaches(profile.sessionTmp)
	if err != nil {
		return nil, nil, err
	}

	profileFile, err := os.CreateTemp(profile.sessionTmp, "wavez-*.sb")
	if err != nil {
		return nil, nil, fmt.Errorf("writing sandbox profile: %w", err)
	}

	if err := writeAndClose(profileFile, profile.Render()); err != nil {
		return nil, nil, err
	}

	cmd := sandboxCommand(ctx, profile.projectRoot, profileFile.Name(), dirs, args)

	//nolint:errcheck // a leftover profile in the session temp dir goes with it
	remove := func() { _ = os.Remove(profileFile.Name()) }

	return cmd, remove, nil
}

// caches are the directories a toolchain writes to that live outside the
// project. Each is redirected into the session's own temp dir, because the
// profile makes the machine's copy readable and not writable.
type caches struct {
	goCache string
	tmp     string
	user    string
	uv      string
}

// secretEnvFragments name the environment variables a sandboxed command
// never needs and must not see. Denying network is not what stops a leak
// here: anything a command prints enters the thread's context, and the next
// hosted turn ships that context to the provider. The API key the daemon
// falls back to when no key command is configured is read from this
// environment, so `echo $OPENROUTER_API_KEY` would have printed it through a
// command the guard allows by name.
var secretEnvFragments = []string{
	"AUTH", "CREDENTIAL", "KEY", "PASSWD", "PASSWORD", "SECRET", "SESSION_TOKEN", "TOKEN",
}

// scrubbedEnv drops every variable whose name carries one of the fragments
// above. It matches on the name and never on the value, so a variable that
// merely holds a secret-looking string still reaches the command: naming is
// the part that is stable enough to decide from.
func scrubbedEnv(env []string) []string {
	out := make([]string, 0, len(env))

	for _, entry := range env {
		name, _, ok := strings.Cut(entry, "=")
		if ok && namesASecret(name) {
			continue
		}

		out = append(out, entry)
	}

	return out
}

func namesASecret(name string) bool {
	upper := strings.ToUpper(name)
	for _, fragment := range secretEnvFragments {
		if strings.Contains(upper, fragment) {
			return true
		}
	}

	return false
}

func prepareCaches(sessionTmp string) (caches, error) {
	dirs := caches{
		goCache: filepath.Join(sessionTmp, "gocache"),
		tmp:     filepath.Join(sessionTmp, "gotmp"),
		user:    filepath.Join(sessionTmp, "cache"),
		uv:      filepath.Join(sessionTmp, "cache", "uv"),
	}
	for _, dir := range []string{dirs.goCache, dirs.tmp, dirs.user, dirs.uv} {
		if err := os.MkdirAll(dir, sessionDirPerm); err != nil {
			return caches{}, fmt.Errorf("preparing sandbox cache dir %q: %w", dir, err)
		}
	}

	return dirs, nil
}

func writeAndClose(f *os.File, content string) error {
	_, writeErr := f.WriteString(content)
	closeErr := f.Close()

	switch {
	case writeErr != nil && closeErr != nil:
		return fmt.Errorf("writing sandbox profile: %w (closing: %w)", writeErr, closeErr)
	case writeErr != nil:
		return fmt.Errorf("writing sandbox profile: %w", writeErr)
	case closeErr != nil:
		return fmt.Errorf("writing sandbox profile: %w", closeErr)
	default:
		return nil
	}
}

func sandboxCommand(
	ctx context.Context, projectRoot, profilePath string, dirs caches, args []string,
) *exec.Cmd {
	sandboxArgs := append([]string{"-f", profilePath}, args...)
	// #nosec G204 -- args is the command this sandbox exists to confine, not passed to an unsandboxed shell.
	cmd := exec.CommandContext(ctx, "sandbox-exec", sandboxArgs...)
	cmd.Dir = projectRoot
	cmd.Env = append(scrubbedEnv(os.Environ()),
		"GOCACHE="+dirs.goCache,
		"GOLANGCI_LINT_CACHE="+dirs.goCache,
		"GOTMPDIR="+dirs.tmp,
		"TMPDIR="+dirs.tmp,
		"GOPROXY=off",
		// uv refuses to run at all when it cannot open its cache, and a
		// Python project's runner is uv the way a Go project's is go.
		"XDG_CACHE_HOME="+dirs.user,
		"UV_CACHE_DIR="+dirs.uv,
	)

	return cmd
}
