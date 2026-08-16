package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/kyleking/agent-locks/internal/analyze"
	"github.com/kyleking/agent-locks/internal/event"
	"github.com/kyleking/agent-locks/internal/hook"
	"github.com/kyleking/agent-locks/internal/lease"
	"github.com/kyleking/agent-locks/internal/store"
)

const usage = `agentlocks — advisory subtree coordination for parallel coding agents

  agentlocks analyze [flags]      mine transcripts for real collision rates
  agentlocks status [flags]       who currently holds what
  agentlocks claim DIR [-m note]  hold a subtree yourself
  agentlocks release DIR          drop your claim
  agentlocks hook EVENT           hook entrypoint (reads stdin)
  agentlocks install              print the settings.json snippet

Events: pre-tool-use, post-tool-use, session-start, session-end
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	args := os.Args[2:]
	switch os.Args[1] {
	case "analyze":
		must(cmdAnalyze(args))
	case "status":
		must(cmdStatus(args))
	case "claim":
		must(cmdClaim(args))
	case "release":
		must(cmdRelease(args))
	case "hook":
		cmdHook(args)
	case "install":
		cmdInstall()
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "agentlocks:", err)
		os.Exit(1)
	}
}

func cmdAnalyze(args []string) error {
	fs := flag.NewFlagSet("analyze", flag.ExitOnError)
	window := fs.String("window", "10m", "collision window")
	since := fs.String("since", "", "only consider writes newer than this (e.g. 30d)")
	projects := fs.String("projects", analyze.DefaultProjectsDir(), "transcript directory")
	top := fs.Int("top", 12, "hotspots to list")
	exact := fs.Bool("exact", false, "count only Edit/Write calls, dropping the Bash heuristic")
	reposOnly := fs.Bool("repos-only", false, "ignore writes outside any git repository")
	asJSON := fs.Bool("json", false, "emit JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	w, err := parseDuration(*window)
	if err != nil {
		return err
	}
	opts := analyze.Options{
		ProjectsDir: *projects, Window: w, TopN: *top,
		ExactOnly: *exact, ReposOnly: *reposOnly,
	}
	if *since != "" {
		d, err := parseDuration(*since)
		if err != nil {
			return err
		}
		opts.Since = time.Now().Add(-d)
	}
	rep, err := analyze.Run(opts)
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(rep)
	}
	printReport(rep)
	return nil
}

func printReport(r *analyze.Report) {
	fmt.Printf("transcripts scanned   %d\n", r.Transcripts)
	fmt.Printf("writes found          %d  (file tools %d, bash %d = %.1f%%)\n",
		r.Writes, r.FileToolWrites, r.BashWrites, r.BashSharePct)
	fmt.Printf("from subagents        %d\n", r.SidechainWrites)
	fmt.Printf("outside session cwd   %d  (%.1f%%)\n", r.OutsideCWD, r.OutsideCWDPct)
	fmt.Printf("outside any git repo  %d\n", r.OutsideRepo)
	fmt.Printf("\ncollisions within %s (a write with another session's write nearby)\n", r.Window)
	fmt.Printf("  directory level     %d  (%.1f%% of writes)\n", r.DirCollisions, r.DirCollisionPct)
	fmt.Printf("  file level          %d\n", r.FileCollisions)
	fmt.Printf("  dirs touched by 2+ sessions ever  %d\n", r.MultiSessionDirs)
	fmt.Printf("\nsame, counting only Edit/Write (no Bash heuristic, so no false positives)\n")
	fmt.Printf("  directory level     %d  (%.1f%% of file-tool writes)\n", r.ExactDirCollisions, r.ExactDirPct)
	fmt.Printf("  file level          %d\n", r.ExactFileCollisions)

	byTool := make([]string, 0, len(r.ByTool))
	for k := range r.ByTool {
		byTool = append(byTool, k)
	}
	sort.Slice(byTool, func(i, j int) bool { return r.ByTool[byTool[i]] > r.ByTool[byTool[j]] })
	fmt.Print("\nby tool               ")
	for _, k := range byTool {
		fmt.Printf("%s=%d  ", k, r.ByTool[k])
	}
	fmt.Println()

	printHotspots("top colliding directories", r.TopDirs, true)
	printHotspots("top colliding files", r.TopFiles, false)
}

func printHotspots(title string, hs []analyze.Hotspot, withSessions bool) {
	if len(hs) == 0 {
		fmt.Printf("\n%s: none\n", title)
		return
	}
	fmt.Printf("\n%s\n", title)
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	for _, h := range hs {
		if withSessions {
			fmt.Fprintf(tw, "  %d\t%s\t(%d sessions)\n", h.Collisions, home(h.Path), len(h.Sessions))
		} else {
			fmt.Fprintf(tw, "  %d\t%s\n", h.Collisions, home(h.Path))
		}
	}
	tw.Flush()
}

func cmdStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "emit JSON")
	all := fs.Bool("all", false, "include expired leases")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := store.Load()
	if err != nil {
		return err
	}
	now := time.Now()
	type row struct {
		lease.Lease
		Strength string `json:"strength"`
	}
	var rows []row
	for _, l := range st.Leases {
		s := l.Strength(now, st.Commits[l.Root], hookTTL())
		if s == lease.StrengthExpired && !*all {
			continue
		}
		rows = append(rows, row{Lease: *l, Strength: s})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Last.After(rows[j].Last) })
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(rows)
	}
	if len(rows) == 0 {
		fmt.Println("no active leases")
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "SUBTREE\tOWNER\tSTATE\tAGE\tWRITES\tROOT")
	for _, r := range rows {
		owner := event.Short(r.Actor)
		if r.Manual {
			owner = "you"
		} else if r.Label != "" && r.Label != "main" {
			owner += " " + r.Label
		}
		note := r.Label
		if r.Manual && note != "" {
			owner = "you: " + note
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\n",
			r.Dir, owner, r.Strength, age(now.Sub(r.Last)), r.Writes, home(r.Root))
	}
	return tw.Flush()
}

// parseClaim accepts the message flag on either side of the directory, because
// "claim DIR -m note" is the order people actually type and flag.Parse stops at the
// first positional argument.
func parseClaim(args []string) (dir, note string, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-m", "--message":
			if i+1 >= len(args) {
				return "", "", fmt.Errorf("%s needs a value", args[i])
			}
			i++
			note = args[i]
		default:
			if strings.HasPrefix(args[i], "-") {
				return "", "", fmt.Errorf("unknown flag %s", args[i])
			}
			if dir != "" {
				return "", "", fmt.Errorf("only one directory at a time")
			}
			dir = args[i]
		}
	}
	return dir, note, nil
}

func cmdClaim(args []string) error {
	arg, note, err := parseClaim(args)
	if err != nil {
		return err
	}
	root, dir, err := resolveArg(arg)
	if err != nil {
		return err
	}
	if err := store.Append(event.Event{
		TS: time.Now(), Kind: event.KindClaim, Owner: event.OwnerHuman,
		Session: event.OwnerHuman, Root: root, Dir: dir, Note: note,
	}); err != nil {
		return err
	}
	fmt.Printf("claimed %s in %s\n", dir, home(root))
	return nil
}

func cmdRelease(args []string) error {
	arg, _, err := parseClaim(args)
	if err != nil {
		return err
	}
	root, dir, err := resolveArg(arg)
	if err != nil {
		return err
	}
	if err := store.Append(event.Event{
		TS: time.Now(), Kind: event.KindRelease, Owner: event.OwnerHuman,
		Session: event.OwnerHuman, Root: root, Dir: dir,
	}); err != nil {
		return err
	}
	fmt.Printf("released %s in %s\n", dir, home(root))
	return nil
}

func resolveArg(arg string) (root, dir string, err error) {
	if arg == "" {
		return "", "", fmt.Errorf("need a directory")
	}
	abs, err := filepath.Abs(arg)
	if err != nil {
		return "", "", err
	}
	t := hook.ResolveDir(abs)
	return t.Root, t.Dir, nil
}

// cmdHook never fails the tool call it is attached to. Any error, panic, or malformed
// input leaves the session untouched.
func cmdHook(args []string) {
	defer func() {
		if r := recover(); r != nil {
			debugf("panic: %v", r)
		}
		os.Exit(0)
	}()
	if len(args) < 1 {
		return
	}
	in, err := hook.ReadInput(os.Stdin)
	if err != nil {
		debugf("bad input: %v", err)
		return
	}
	now := time.Now()
	switch args[0] {
	case "pre-tool-use":
		hook.EmitContext(os.Stdout, "PreToolUse", hook.PreToolUse(in, now))
	case "post-tool-use":
		hook.PostToolUse(in, now)
	case "session-start":
		hook.SessionStart(in, now)
	case "session-end":
		hook.SessionEnd(in, now)
	default:
		debugf("unknown hook event %q", args[0])
	}
}

func debugf(format string, a ...any) {
	if os.Getenv("AGENT_LOCKS_DEBUG") != "" {
		fmt.Fprintf(os.Stderr, "agentlocks: "+format+"\n", a...)
	}
}

func cmdInstall() {
	fmt.Print(`Add to ~/.claude/settings.json (merge with existing hooks):

{
  "hooks": {
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "agentlocks hook session-start", "timeout": 5}]}
    ],
    "SessionEnd": [
      {"hooks": [{"type": "command", "command": "agentlocks hook session-end", "timeout": 5}]}
    ],
    "PreToolUse": [
      {"matcher": "Edit|Write|MultiEdit|NotebookEdit|Bash",
       "hooks": [{"type": "command", "command": "agentlocks hook pre-tool-use", "timeout": 5}]}
    ],
    "PostToolUse": [
      {"matcher": "Edit|Write|MultiEdit|NotebookEdit|Bash",
       "hooks": [{"type": "command", "command": "agentlocks hook post-tool-use", "timeout": 5}]}
    ]
  }
}

State lives in ` + store.Dir() + ` (override with AGENT_LOCKS_DIR).
`)
}

func hookTTL() time.Duration {
	if v := os.Getenv("AGENT_LOCKS_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return hook.DefaultTTL
}

func parseDuration(s string) (time.Duration, error) {
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		if err != nil {
			return 0, err
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

func age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func home(p string) string {
	h, err := os.UserHomeDir()
	if err != nil || !strings.HasPrefix(p, h) {
		return p
	}
	return "~" + strings.TrimPrefix(p, h)
}
