// Package main implements voice-cli: CLI for importing and reviewing personal
// writing samples (iMessage, Mail, Linear) into a voice-guide corpus for
// Claude Code.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kyleking/voice-cli/internal/corpus"
	"github.com/kyleking/voice-cli/internal/review"
	"github.com/kyleking/voice-cli/internal/sources"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-v", "--version":
			fmt.Printf("voice-cli %s (commit: %s, built: %s)\n", version, commit, date)
			os.Exit(0)
		case "-h", "--help":
			printHelp()
			os.Exit(0)
		}
	}

	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printHelp()
		return nil
	}

	switch args[0] {
	case "import":
		return runImport(args[1:])
	default:
		printHelp()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runImport(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("import requires a source: linear, imessage, or mail")
	}

	switch args[0] {
	case "linear":
		return runImportLinear(args[1:])
	case "imessage":
		return runImportIMessage(args[1:])
	case "mail":
		return runImportMail(args[1:])
	default:
		return fmt.Errorf("unknown import source %q (want linear, imessage, or mail)", args[0])
	}
}

func runImportLinear(args []string) error {
	fs := flag.NewFlagSet("import linear", flag.ExitOnError)
	out := fs.String("out", "", "path to the .jsonl corpus file to append kept candidates to (required)")
	meUserID := fs.String("me-user-id", "", "your Linear user id, used to partition issues/comments into personal vs company (required)")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), `Usage: voice-cli import linear -out FILE -me-user-id ID

Fetches your 50 most recent Linear issues (with descriptions and comments) via
the GraphQL API and reviews each before appending kept candidates to -out.

Reads the Linear personal API key from the LINEAR_API_KEY environment
variable.

Limitation: fetches a single page of 50 issues; deeper history requires
cursor-based pagination, not yet implemented.
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" || *meUserID == "" {
		fs.Usage()
		return fmt.Errorf("both -out and -me-user-id are required")
	}

	apiKey := os.Getenv("LINEAR_API_KEY")
	if apiKey == "" {
		return fmt.Errorf("LINEAR_API_KEY environment variable is not set")
	}

	candidates, err := sources.FetchLinear(apiKey, *meUserID)
	if err != nil {
		return fmt.Errorf("fetching linear candidates: %w", err)
	}

	return reviewAndAppend(candidates, *out)
}

func runImportIMessage(args []string) error {
	fs := flag.NewFlagSet("import imessage", flag.ExitOnError)
	out := fs.String("out", "", "path to the .jsonl corpus file to append kept candidates to (required)")
	dbPath := fs.String("db", defaultIMessageDBPath(), "path to Messages chat.db")
	limit := fs.Int("limit", 200, "maximum number of outgoing messages to fetch")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), `Usage: voice-cli import imessage -out FILE [-db PATH] [-limit N]

Reads chat.db read-only and fetches outgoing (is_from_me = 1) messages with
plain-text bodies. Requires Full Disk Access granted to the terminal running
this command.

Limitation: messages stored only as attributedBody (rich text, requiring
NSKeyedArchiver/typedstream decoding) are skipped; the skipped count is
printed.
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		fs.Usage()
		return fmt.Errorf("-out is required")
	}

	candidates, err := sources.FetchIMessages(*dbPath, *limit)
	if err != nil {
		return fmt.Errorf("fetching imessage candidates: %w", err)
	}

	return reviewAndAppend(candidates, *out)
}

func runImportMail(args []string) error {
	fs := flag.NewFlagSet("import mail", flag.ExitOnError)
	out := fs.String("out", "", "path to the .jsonl corpus file to append kept candidates to (required)")
	mailDir := fs.String("mail-dir", defaultMailDir(), "path to Apple Mail's local mail directory")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), `Usage: voice-cli import mail -out FILE [-mail-dir PATH]

Walks -mail-dir for .emlx files under paths containing "Sent" and parses each
into a candidate.

Limitation: multipart bodies get best-effort text/plain extraction, not full
MIME fidelity.
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		fs.Usage()
		return fmt.Errorf("-out is required")
	}

	candidates, err := sources.FetchMail(*mailDir)
	if err != nil {
		return fmt.Errorf("fetching mail candidates: %w", err)
	}

	return reviewAndAppend(candidates, *out)
}

func reviewAndAppend(candidates []corpus.Candidate, out string) error {
	reviewer := review.NewReviewer(os.Stdin, os.Stdout)
	kept, err := reviewer.Review(candidates)
	if err != nil {
		return fmt.Errorf("reviewing candidates: %w", err)
	}

	if err := corpus.Append(out, kept); err != nil {
		return fmt.Errorf("writing corpus: %w", err)
	}

	fmt.Printf("kept %d of %d candidate(s), appended to %s\n", len(kept), len(candidates), out)
	return nil
}

func defaultIMessageDBPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Library", "Messages", "chat.db")
}

func defaultMailDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "Library", "Mail")
}

func printHelp() {
	fmt.Print(`voice-cli - CLI for importing and reviewing personal writing samples (iMessage, Mail, Linear) into a voice-guide corpus for Claude Code

Usage:
  voice-cli import linear   -out FILE -me-user-id ID
  voice-cli import imessage -out FILE [-db PATH] [-limit N]
  voice-cli import mail     -out FILE [-mail-dir PATH]

Every imported candidate is shown for review before being appended to -out:
  k          keep as-is
  e text...  keep, replacing the text with the given redacted text
  s          skip
  q          quit early (candidates decided so far are still saved)

Options:
  -h, --help     Show this help message
  -v, --version  Show version information

Run "voice-cli import <source> -h" for source-specific flags and limitations.
`)
}
