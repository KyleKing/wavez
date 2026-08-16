package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
// sessionTmp, with GOCACHE, GOMODCACHE, and GOTMPDIR redirected into
// sessionTmp. It fails closed: any error resolving paths or building the
// profile aborts before sandbox-exec is invoked, and a nonzero exit from
// the sandboxed command is reported in Result rather than as an error.
func Exec(ctx context.Context, projectRoot, sessionTmp string, args ...string) (Result, error) {
	if len(args) == 0 {
		return Result{}, ErrNoCommand
	}

	profile, err := NewProfile(projectRoot, sessionTmp)
	if err != nil {
		return Result{}, fmt.Errorf("building sandbox profile: %w", err)
	}

	caches, err := prepareGoCaches(profile.sessionTmp)
	if err != nil {
		return Result{}, err
	}

	profileFile, err := os.CreateTemp(profile.sessionTmp, "wavez-*.sb")
	if err != nil {
		return Result{}, fmt.Errorf("writing sandbox profile: %w", err)
	}
	if err := writeAndClose(profileFile, profile.Render()); err != nil {
		return Result{}, err
	}

	result, runErr := runSandboxed(ctx, profile.projectRoot, profileFile.Name(), caches, args)

	if rmErr := os.Remove(profileFile.Name()); rmErr != nil {
		if runErr != nil {
			return Result{}, runErr
		}

		return Result{}, fmt.Errorf("removing sandbox profile: %w", rmErr)
	}

	return result, runErr
}

type goCaches struct {
	cache    string
	modCache string
	tmp      string
}

func prepareGoCaches(sessionTmp string) (goCaches, error) {
	caches := goCaches{
		cache:    filepath.Join(sessionTmp, "gocache"),
		modCache: filepath.Join(sessionTmp, "gomodcache"),
		tmp:      filepath.Join(sessionTmp, "gotmp"),
	}
	for _, dir := range []string{caches.cache, caches.modCache, caches.tmp} {
		if err := os.MkdirAll(dir, sessionDirPerm); err != nil {
			return goCaches{}, fmt.Errorf("preparing sandbox go cache dir %q: %w", dir, err)
		}
	}

	return caches, nil
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

func runSandboxed(
	ctx context.Context, projectRoot, profilePath string, caches goCaches, args []string,
) (Result, error) {
	sandboxArgs := append([]string{"-f", profilePath}, args...)
	// #nosec G204 -- args is the command this sandbox exists to confine, not passed to an unsandboxed shell.
	cmd := exec.CommandContext(ctx, "sandbox-exec", sandboxArgs...)
	cmd.Dir = projectRoot
	cmd.Env = append(os.Environ(),
		"GOCACHE="+caches.cache,
		"GOMODCACHE="+caches.modCache,
		"GOTMPDIR="+caches.tmp,
	)

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
