package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/kyleking/wavez/internal/bench"
	"github.com/kyleking/wavez/internal/goal"
	"github.com/kyleking/wavez/internal/hook"
	page "github.com/kyleking/wavez/internal/report"
	"github.com/kyleking/wavez/internal/session"
	"github.com/kyleking/wavez/internal/transcript"
)

// reportUsage is the command line of `wavez report`.
const reportUsage = "usage: wavez report [--dir DIR] [--out FILE] [--open=false] [--full] [--text] [--bench] " +
	"[<session-id | path/to/transcript.jsonl | path/to/records.jsonl>]"

// Where reports are written, and how.
const (
	reportsDir     = "reports"
	reportDirMode  = 0o750
	reportFileMode = 0o600
	maxTraceLine   = 1 << 20
)

type reportOptions struct {
	dir   string
	out   string
	trace string
	arg   string
	open  bool
	full  bool
	text  bool
	bench bool
}

// runReport writes one page saying how a session went: what needs the
// person, where the turns went, and what to say next time. With no session
// named it takes the most recent one that ran in the project.
func runReport(ctx context.Context, args []string, streams stdio) error {
	opts, err := parseReport(args, streams.err)
	if err != nil {
		return err
	}

	root, err := filepath.Abs(hook.FindRoot(opts.dir))
	if err != nil {
		return fmt.Errorf("resolving %s: %w", opts.dir, err)
	}

	if opts.bench {
		return reportBench(ctx, root, &opts, streams)
	}

	path, err := reportTranscript(root, opts.arg)
	if err != nil {
		return err
	}

	var read []transcript.TimelineOption
	if opts.full {
		read = append(read, transcript.WithDetail())
	}

	tl, err := transcript.ReadTimeline(path, read...)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	if tl.SessionID == "" {
		tl.SessionID = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}

	in := page.Input{
		Timeline: tl,
		Project:  filepath.Base(root),
		Goal:     goalProgress(root),
		State:    sessionState(root, tl.SessionID),
		Tally:    hookTally(opts.trace, tl.SessionID),
		Full:     opts.full,
	}

	if tl.Cwd != "" {
		in.Project = filepath.Base(hook.FindRoot(tl.Cwd))
	}

	built := page.Build(&in)

	if opts.text {
		return textPage(&built, streams.out)
	}

	out := opts.out
	if out == "" {
		out = filepath.Join(root, session.Dir, reportsDir, tl.SessionID+".html")
	}

	return writePage(ctx, out, root, opts.open, streams, built.HTML)
}

func parseReport(args []string, stderr io.Writer) (reportOptions, error) {
	var opts reportOptions

	flags := flag.NewFlagSet("report", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&opts.dir, "dir", ".", "the project the session ran in")
	flags.StringVar(&opts.out, "out", "", "where to write the page (default: .wavez/reports/<session>.html)")
	flags.StringVar(&opts.trace, "trace", os.Getenv(hook.TraceEnv), "a hook trace to count hints and denials from")
	flags.BoolVar(&opts.open, "open", true, "open the page in a browser when run from a terminal")
	flags.BoolVar(&opts.full, "full", false, "include whole commands, prompts, and hook texts")
	flags.BoolVar(&opts.text, "text", false, "print the report as plain text instead of writing a page")
	flags.BoolVar(&opts.bench, "bench", false, "report the benchmark records instead of a session")

	if err := flags.Parse(args); err != nil {
		return opts, exitError(usageStatus)
	}

	if flags.NArg() > 1 {
		return opts, &usageError{msg: reportUsage}
	}

	opts.arg = flags.Arg(0)

	return opts, nil
}

// reportTranscript finds the transcript to report on: the one named, or
// the newest of a session that ran in the project.
func reportTranscript(root, arg string) (string, error) {
	if arg != "" {
		return transcriptPath(arg)
	}

	path, err := transcript.Latest(transcriptRoot(), root)
	if err != nil {
		return "", fmt.Errorf("finding the latest session: %w", err)
	}

	return path, nil
}

func textPage(built *page.Page, w io.Writer) error {
	if err := built.Text(w); err != nil {
		return fmt.Errorf("printing the report: %w", err)
	}

	return nil
}

// goalProgress reads the project's goal as its last end-of-turn check left
// it, and returns nil when there is none or it cannot be read.
func goalProgress(root string) *page.Goal {
	sum, err := goal.Snapshot(root)
	if err != nil {
		return nil
	}

	return &page.Goal{Sentence: sum.Goal, Unmet: sum.Unmet, Total: sum.Total, Done: sum.Done}
}

// sessionState reads what wavez's hooks kept about the session, and
// returns nil when they kept nothing: the page then says so instead of
// showing a zero.
func sessionState(root, id string) *page.State {
	state, ok := session.NewStore(root).Read(id)
	if !ok {
		return nil
	}

	return &page.State{Changed: len(state.Recorded), FailingRounds: state.Failure.Rounds}
}

// hookTally counts the hints and denials a hook trace holds for the
// session. A trace that is missing or unreadable counts for nothing.
func hookTally(path, id string) *page.Tally {
	if path == "" || id == "" {
		return nil
	}

	f, err := os.Open(path) //nolint:gosec // the owner names the trace to read
	if err != nil {
		return nil
	}

	var own bytes.Buffer

	lines := bufio.NewScanner(f)
	lines.Buffer(nil, maxTraceLine)

	for lines.Scan() {
		var rec struct {
			Session string `json:"session"`
		}

		if json.Unmarshal(lines.Bytes(), &rec) == nil && rec.Session == id {
			own.Write(lines.Bytes())
			own.WriteByte('\n')
		}
	}

	if errors.Join(lines.Err(), f.Close()) != nil {
		return nil
	}

	tally, err := hook.ReadTally(&own)
	if err != nil {
		return nil
	}

	return &page.Tally{RouteHints: tally.RouteHints, RenameDenials: tally.RenameDenials}
}

// writePage renders a page to out, prints where it went, and opens it when
// asked to from a terminal.
func writePage(
	ctx context.Context, out, root string, open bool, streams stdio, render func(io.Writer) error,
) error {
	var buf bytes.Buffer
	if err := render(&buf); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(out), reportDirMode); err != nil {
		return fmt.Errorf("making %s: %w", filepath.Dir(out), err)
	}

	if err := ignoreState(root, out); err != nil {
		return err
	}

	if err := os.WriteFile(out, buf.Bytes(), reportFileMode); err != nil {
		return fmt.Errorf("writing the report: %w", err)
	}

	if _, err := fmt.Fprintln(streams.out, out); err != nil {
		return fmt.Errorf("printing the report's path: %w", err)
	}

	if open && isTerminal(streams.out) {
		openInBrowser(ctx, out)
	}

	return nil
}

// ignoreState keeps a report written under the project's .wavez out of
// version control, the way `wavez init` does, when nothing has yet.
func ignoreState(root, out string) error {
	if !below(filepath.Join(root, session.Dir), out) {
		return nil
	}

	ignore := filepath.Join(root, filepath.FromSlash(hook.GitignorePath))
	if _, err := os.Stat(ignore); err == nil {
		return nil
	}

	if err := os.WriteFile(ignore, []byte("*\n"), reportFileMode); err != nil {
		return fmt.Errorf("writing %s: %w", ignore, err)
	}

	return nil
}

// below reports whether path lies under dir.
func below(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)

	return err == nil && filepath.IsLocal(rel)
}

// isTerminal reports whether w is a terminal, so a report run by an agent
// or into a pipe never starts a browser.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}

	info, err := f.Stat()

	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// openInBrowser asks the desktop to show the page. A machine with no
// opener, or no desktop, is left with the printed path.
func openInBrowser(ctx context.Context, path string) {
	name, args := "xdg-open", []string{path}

	switch runtime.GOOS {
	case "darwin":
		name = "open"
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler", path}
	}

	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // a fixed opener and the page this command wrote
	if err := cmd.Start(); err != nil {
		return
	}

	// The opener is left to finish on its own; its status changes nothing.
	go func() {
		if err := cmd.Wait(); err != nil {
			return
		}
	}()
}

// reportBench writes the benchmark records as a page: each task's arms
// side by side. The argument names the records file.
func reportBench(ctx context.Context, root string, opts *reportOptions, streams stdio) error {
	if opts.text || opts.full {
		return &usageError{msg: "--bench writes a page only; `wavez bench report` prints the records as text"}
	}

	source := opts.arg
	if source == "" {
		source = filepath.Join(root, filepath.FromSlash(defaultRecords))
	}

	recs, err := bench.LoadRecords(source)
	if err != nil {
		return fmt.Errorf("loading records: %w", err)
	}

	built := page.BuildBench(source, recs)

	out := opts.out
	if out == "" {
		out = filepath.Join(root, session.Dir, reportsDir, "bench.html")
	}

	return writePage(ctx, out, root, opts.open, streams, built.HTML)
}
