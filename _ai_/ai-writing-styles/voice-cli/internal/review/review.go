// Package review implements the human-in-the-loop gate that every imported
// candidate must pass before it can be written into the voice corpus.
package review

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/kyleking/voice-cli/internal/corpus"
)

// Reviewer prompts a human, one candidate at a time, to keep, edit, skip, or
// quit. It reads/writes through the given io.Reader/io.Writer so it can be
// driven by os.Stdin/os.Stdout in production and by in-memory buffers in tests.
type Reviewer struct {
	In  io.Reader
	Out io.Writer
}

func NewReviewer(in io.Reader, out io.Writer) *Reviewer {
	return &Reviewer{In: in, Out: out}
}

// Review walks candidates in order, printing each with its source context and
// reading one line of operator input:
//
//	k         keep as-is
//	e text... keep, replacing Text with the given redacted text
//	s         skip
//	q         quit early; candidates decided so far are still returned
func (r *Reviewer) Review(candidates []corpus.Candidate) ([]corpus.Candidate, error) {
	scanner := bufio.NewScanner(r.In)
	kept := make([]corpus.Candidate, 0, len(candidates))

	for i, c := range candidates {
		fmt.Fprintf(r.Out, "\n[%d/%d] source=%s author=%s context=%s\n%s\n",
			i+1, len(candidates), c.Source, c.Author, c.Context, c.Text)
		fmt.Fprint(r.Out, "[k]eep / [e]dit <text> / [s]kip / [q]uit> ")

		if !scanner.Scan() {
			break
		}
		line := scanner.Text()

		switch {
		case line == "k":
			kept = append(kept, c)
		case line == "s":
			continue
		case line == "q":
			return kept, nil
		case strings.HasPrefix(line, "e "):
			edited := c
			edited.Text = strings.TrimPrefix(line, "e ")
			edited.Redacted = true
			kept = append(kept, edited)
		default:
			fmt.Fprintf(r.Out, "unrecognized input %q, skipping candidate\n", line)
		}
	}

	if err := scanner.Err(); err != nil {
		return kept, fmt.Errorf("reading review input: %w", err)
	}
	return kept, nil
}
