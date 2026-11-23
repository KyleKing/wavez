package gate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const (
	// The headCap and tailCap bound what one command may hand back. A
	// failing test's name is printed before what it logged and the
	// package's verdict after, so both ends are kept and the middle is what
	// goes when a run prints megabytes.
	headCap = 256 << 10
	tailCap = 768 << 10
	// The killGrace is how long a canceled command's descendants may hold its
	// output pipes open before the wait gives up on them.
	killGrace = 2 * time.Second
	// The reasonLimit bounds the one line an abstention spends on its reason.
	reasonLimit = 200

	exitNotExecutable = 126
	exitNotFound      = 127

	// The reasonCanceled is why a command the caller interrupted gave no
	// answer, and reasonOutOfTime why one the run's own budget ended did.
	reasonCanceled  = "canceled"
	reasonOutOfTime = "the gate's time budget ran out first"
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
	dir  string
	name string
	args []string
	// env is added to the process's own environment.
	env     []string
	timeout time.Duration
}

// run executes inv to completion, its deadline, or ctx's end, whichever
// comes first. The command gets its own process group so that a deadline
// takes the whole tree down, not only the shell at its root.
func run(ctx context.Context, inv invocation) outcome {
	if ctx.Err() != nil {
		return outcome{reason: ended(ctx)}
	}

	timed, cancel := context.WithTimeout(ctx, inv.timeout)
	defer cancel()

	var buf endsBuffer

	//nolint:gosec // running the project's own configured checks is the purpose of this package
	cmd := exec.CommandContext(timed, inv.name, inv.args...)
	cmd.Dir = inv.dir
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	cmd.WaitDelay = killGrace

	if len(inv.env) > 0 {
		cmd.Env = append(os.Environ(), inv.env...)
	}

	ownGroup(cmd)

	err := cmd.Run()
	out := outcome{output: buf.String()}

	var exit *exec.ExitError

	switch {
	case err == nil:
	case ctx.Err() != nil:
		out.reason = ended(ctx)
	case errors.Is(timed.Err(), context.DeadlineExceeded):
		out.reason = fmt.Sprintf("timed out after %s", inv.timeout)
	case errors.As(err, &exit) && exit.ExitCode() >= 0:
		out.exit = exit.ExitCode()
	default:
		out.reason = oneLine(err.Error())
	}

	return out
}

// ended says why a context that is over is over.
func ended(ctx context.Context) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return reasonOutOfTime
	}

	return reasonCanceled
}

// endsBuffer keeps the first headCap bytes written to it and the last
// tailCap, and drops whole lines between them.
type endsBuffer struct {
	head bytes.Buffer
	tail bytes.Buffer
	cut  bool
}

func (b *endsBuffer) Write(p []byte) (int, error) {
	rest := p

	if room := headCap - b.head.Len(); room > 0 {
		n := min(room, len(rest))
		b.head.Write(rest[:n])
		rest = rest[n:]
	}

	b.tail.Write(rest)

	if over := b.tail.Len() - tailCap; over > 0 {
		b.tail.Next(over)
		b.cut = true
	}

	return len(p), nil
}

func (b *endsBuffer) String() string {
	if !b.cut {
		return b.head.String() + b.tail.String()
	}

	head, tail := b.head.String(), b.tail.String()

	// Both cuts fall mid-line; the halves of those two lines go.
	if i := strings.LastIndexByte(head, '\n'); i >= 0 {
		head = head[:i+1]
	}

	if i := strings.IndexByte(tail, '\n'); i >= 0 {
		tail = tail[i+1:]
	}

	return head + tail
}

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
