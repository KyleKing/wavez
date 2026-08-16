// Package vcs abstracts git and jj repository operations behind a common Operations interface.
package vcs

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type commandRunner func(ctx context.Context, dir, name string, args ...string) (string, error)

type commandRunnerKey struct{}

// withCommandRunner returns a context that makes runCommand call fn instead of
// executing a real subprocess. Used by tests to stub git/jj invocations
// without touching shared package state, so subtests can run in parallel.
func withCommandRunner(ctx context.Context, fn commandRunner) context.Context {
	return context.WithValue(ctx, commandRunnerKey{}, fn)
}

func runCommand(ctx context.Context, dir, name string, args ...string) (string, error) {
	out, err := runCommandRaw(ctx, dir, name, args...)
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(out), nil
}

// runCommandRaw returns stdout verbatim. Parsers of NUL-delimited output need
// it: the leading byte of the first record carries meaning (a porcelain status
// code's unstaged column is a leading space) that runCommand's trim eats.
func runCommandRaw(ctx context.Context, dir, name string, args ...string) (string, error) {
	if fn, ok := ctx.Value(commandRunnerKey{}).(commandRunner); ok {
		return fn(ctx, dir, name, args...)
	}

	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- name/args from internal command tables
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("running %s: %w", name, err)
	}

	return string(out), nil
}
