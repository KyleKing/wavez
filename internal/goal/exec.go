package goal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

const (
	// The outputCap bounds what one process may hand back. The tail is the
	// part kept, because a long run ends in its verdict.
	outputCap = 1 << 20
	// The killGrace is how long a killed process's descendants may hold its
	// output pipes open before the wait gives up on them.
	killGrace = 2 * time.Second
	// The detailLimit bounds the one line a condition spends on its detail.
	detailLimit = 160

	exitNotExecutable = 126
	exitNotFound      = 127
)

// outcome is what running one process established. A non-empty reason means
// it never gave an answer, whatever it printed.
type outcome struct {
	output string
	reason string
	exit   int
}

// run executes one process in dir to completion, its timeout, or ctx's
// cancellation. The process leads its own group, so a timeout takes down
// everything it started and not only the shell at its root.
func run(ctx context.Context, dir string, timeout time.Duration, name string, args ...string) outcome {
	if ctx.Err() != nil {
		return outcome{reason: reasonCanceled}
	}

	timed, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var buf tailBuffer

	//nolint:gosec // running what the person's own goal file names is the purpose of this package
	cmd := exec.CommandContext(timed, name, args...)
	cmd.Dir = dir
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = killGrace
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }

	err := cmd.Run()
	out := outcome{output: buf.String()}

	var exit *exec.ExitError

	switch {
	case err == nil:
	case ctx.Err() != nil:
		out.reason = reasonCanceled
	case errors.Is(timed.Err(), context.DeadlineExceeded):
		out.reason = fmt.Sprintf("timed out after %s", timeout)
	case errors.Is(err, exec.ErrNotFound):
		out.reason = name + " is not on PATH"
	case errors.As(err, &exit) && exit.ExitCode() >= 0:
		out.exit = exit.ExitCode()
	default:
		out.reason = oneLine(err.Error(), "it could not be started")
	}

	return out
}

const reasonCanceled = "canceled"

// tailBuffer keeps the last outputCap bytes written to it.
type tailBuffer struct {
	buf bytes.Buffer
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	n, err := t.buf.Write(p)
	if over := t.buf.Len() - outputCap; over > 0 {
		t.buf.Next(over)
	}

	return n, err //nolint:wrapcheck // bytes.Buffer never fails a write; the signature is io.Writer's
}

func (t *tailBuffer) String() string { return t.buf.String() }

// oneLine reduces text to its first line that says something, bounded, or
// to fallback when no line does. A `#` line is go's package header and
// names nothing the reader can act on.
func oneLine(text, fallback string) string {
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			return clip(line, detailLimit)
		}
	}

	return fallback
}

// clip cuts text to at most limit bytes on a rune boundary, marking the cut.
func clip(text string, limit int) string {
	const mark = "..."

	if len(text) <= limit {
		return text
	}

	cut := max(limit-len(mark), 0)
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}

	return text[:cut] + mark
}
