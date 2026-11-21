package gate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

const (
	// The outputCap bounds what one command may hand back. A test suite that
	// prints megabytes still ends in its verdict, so the tail is the part
	// kept when the cap is hit.
	outputCap = 1 << 20
	// The killGrace is how long a canceled command's descendants may hold its
	// output pipes open before the wait gives up on them.
	killGrace = 2 * time.Second
	// The reasonLimit bounds the one line an abstention spends on its reason.
	reasonLimit = 200

	exitNotExecutable = 126
	exitNotFound      = 127
)

// outcome is what running one command established. A non-empty reason means
// the command never gave an answer, which is an abstention whatever it
// printed; otherwise exit and output are the answer.
type outcome struct {
	output string
	reason string
	exit   int
}

// invocation is one command line and where it runs.
type invocation struct {
	dir     string
	name    string
	args    []string
	timeout time.Duration
}

// run executes inv to completion, its deadline, or ctx's cancellation,
// whichever comes first. The command gets its own process group so that a
// deadline takes the whole tree down, not only the shell at its root.
func run(ctx context.Context, inv invocation) outcome {
	if ctx.Err() != nil {
		return outcome{reason: "canceled"}
	}

	timed, cancel := context.WithTimeout(ctx, inv.timeout)
	defer cancel()

	var buf tailBuffer

	//nolint:gosec // running the project's own configured checks is the purpose of this package
	cmd := exec.CommandContext(timed, inv.name, inv.args...)
	cmd.Dir = inv.dir
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
		out.reason = "canceled"
	case errors.Is(timed.Err(), context.DeadlineExceeded):
		out.reason = fmt.Sprintf("timed out after %s", inv.timeout)
	case errors.As(err, &exit) && exit.ExitCode() >= 0:
		out.exit = exit.ExitCode()
	default:
		out.reason = oneLine(err.Error())
	}

	return out
}

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

// oneLine reduces text to its first line that says something, bounded, for
// use as an abstention's reason. A `#` line is go's package header and names
// nothing the reader can act on.
func oneLine(text string) string {
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		if len(line) > reasonLimit {
			return line[:reasonLimit] + "..."
		}

		return line
	}

	return "it failed without saying why"
}

// shellQuote joins paths as single-quoted shell words.
func shellQuote(paths []string) string {
	quoted := make([]string, len(paths))
	for i, p := range paths {
		quoted[i] = "'" + strings.ReplaceAll(p, "'", `'\''`) + "'"
	}

	return strings.Join(quoted, " ")
}
