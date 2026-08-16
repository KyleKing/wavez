package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kyleking/agent-locks/internal/bashwrite"
	"github.com/kyleking/agent-locks/internal/event"
	"github.com/kyleking/agent-locks/internal/lease"
	"github.com/kyleking/agent-locks/internal/store"
)

const (
	DefaultTTL      = 30 * time.Minute
	DefaultCooldown = 10 * time.Minute
)

type Input struct {
	HookEventName string `json:"hook_event_name"`
	SessionID     string `json:"session_id"`
	AgentID       string `json:"agent_id"`
	AgentType     string `json:"agent_type"`
	CWD           string `json:"cwd"`
	ToolName      string `json:"tool_name"`
	ToolInput     struct {
		FilePath     string `json:"file_path"`
		NotebookPath string `json:"notebook_path"`
		Command      string `json:"command"`
	} `json:"tool_input"`
	ToolResponse json.RawMessage `json:"tool_response"`
	Reason       string          `json:"reason"`
	Source       string          `json:"source"`
}

type Target struct {
	Root string
	Dir  string
	Path string
}

var fileTools = map[string]bool{
	"Edit": true, "Write": true, "MultiEdit": true, "NotebookEdit": true,
}

func ReadInput(r io.Reader) (*Input, error) {
	var in Input
	if err := json.NewDecoder(r).Decode(&in); err != nil {
		return nil, err
	}
	return &in, nil
}

func (in *Input) actor() string { return event.Actor(in.SessionID, in.AgentID) }

func (in *Input) label() string {
	if in.AgentType != "" {
		return in.AgentType
	}
	return "main"
}

// Targets resolves the subtrees a tool call would write to. Attribution is keyed on the
// write's Target path rather than the session's working directory, because sessions
// routinely write outside their own cwd by absolute path.
func (in *Input) Targets() []Target {
	switch {
	case fileTools[in.ToolName]:
		p := in.ToolInput.FilePath
		if p == "" {
			p = in.ToolInput.NotebookPath
		}
		if p == "" {
			return nil
		}
		return []Target{resolve(p, in.CWD)}
	case in.ToolName == "Bash":
		res := bashwrite.Detect(in.ToolInput.Command)
		if !res.IsWrite {
			return nil
		}
		if len(res.Paths) == 0 {
			return []Target{resolve(in.CWD, in.CWD)}
		}
		seen := map[string]bool{}
		var out []Target
		for _, p := range res.Paths {
			t := resolve(p, in.CWD)
			k := t.Root + "\x00" + t.Dir
			if !seen[k] {
				seen[k] = true
				out = append(out, t)
			}
		}
		return out
	}
	return nil
}

func resolve(path, cwd string) Target {
	if !filepath.IsAbs(path) {
		path = filepath.Join(filepath.Clean(cwd), path)
	}
	path = filepath.Clean(path)
	dir, name := path, ""
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		dir, name = filepath.Split(path)
		dir = filepath.Clean(dir)
	}
	dir = realpath(dir)
	root := gitRoot(dir)
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		rel = "."
	}
	return Target{Root: root, Dir: rel, Path: filepath.Join(dir, name)}
}

// realpath collapses symlinks so one directory reached by two paths yields one key. On
// macOS /tmp and /var are symlinks into /private, so sessions reporting either spelling
// would otherwise hold leases that never match each other.
func realpath(dir string) string {
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		return r
	}
	return dir
}

// gitRoot walks up for a .git entry rather than shelling out, since this runs on every
// matching tool call.
func gitRoot(dir string) string {
	for d := dir; ; {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return dir
		}
		d = parent
	}
}

func ttl() time.Duration      { return durEnv("AGENT_LOCKS_TTL", DefaultTTL) }
func cooldown() time.Duration { return durEnv("AGENT_LOCKS_COOLDOWN", DefaultCooldown) }

func durEnv(name string, def time.Duration) time.Duration {
	if v := os.Getenv(name); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

// PreToolUse reports contention without changing the permission outcome. It never
// returns a permissionDecision: emitting "allow" here would bypass the user's own
// permission prompts for every edit, which is a wider grant than this tool needs.
func PreToolUse(in *Input, now time.Time) string {
	targets := in.Targets()
	if len(targets) == 0 {
		return ""
	}
	st, err := store.Load()
	if err != nil {
		return ""
	}
	var notes []string
	for _, t := range targets {
		for _, c := range contenders(st, in.actor(), t, now) {
			if now.Sub(store.LastWarn(st, in.actor(), t.Root, t.Dir, c.Actor)) < cooldown() {
				continue
			}
			notes = append(notes, describe(c, t, st, now))
			_ = store.Append(event.Event{
				TS: now, Kind: event.KindWarn, Owner: event.OwnerAgent,
				Session: in.SessionID, Agent: in.AgentID,
				Root: t.Root, Dir: t.Dir, Peer: c.Actor,
			})
		}
	}
	if len(notes) == 0 {
		return ""
	}
	return "[agent-locks] " + strings.Join(notes, "\n[agent-locks] ") +
		"\nProceed if your change is independent. If it is not, say so and stop rather than editing around them."
}

func contenders(st *store.State, me string, t Target, now time.Time) []lease.Lease {
	var out []lease.Lease
	for _, l := range st.Leases {
		if l.Actor == me || l.Root != t.Root || !lease.Overlaps(t.Dir, l.Dir) {
			continue
		}
		if l.Strength(now, st.Commits[l.Root], ttl()) == lease.StrengthExpired {
			continue
		}
		out = append(out, *l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Last.After(out[j].Last) })
	return out
}

func describe(l lease.Lease, t Target, st *store.State, now time.Time) string {
	ago := now.Sub(l.Last).Round(time.Second)
	switch l.Strength(now, st.Commits[l.Root], ttl()) {
	case lease.StrengthManual:
		note := ""
		if l.Label != "" {
			note = fmt.Sprintf(" (%q)", l.Label)
		}
		return fmt.Sprintf("%s is manually claimed by the user%s, held %s. Ask before editing here.",
			l.Dir, note, ago)
	case lease.StrengthCommitted:
		return fmt.Sprintf("%s has committed work from session %s (%d writes, last %s ago). Rebase risk rather than an edit conflict.",
			l.Dir, event.Short(l.Actor), l.Writes, ago)
	default:
		who := event.Short(l.Actor)
		if l.Label != "" {
			who += " " + l.Label
		}
		return fmt.Sprintf("%s is being edited by session %s right now (%d writes, last %s ago).",
			l.Dir, who, l.Writes, ago)
	}
}

// PostToolUse records what actually happened. Writes are logged here rather than in
// PreToolUse so a blocked or failed tool call never creates a lease.
func PostToolUse(in *Input, now time.Time) {
	if in.ToolName == "Bash" && bashwrite.IsGitCommit(in.ToolInput.Command) && !toolFailed(in) {
		root := rootOf(in.CWD)
		_ = store.Append(event.Event{
			TS: now, Kind: event.KindCommit, Owner: event.OwnerAgent,
			Session: in.SessionID, Agent: in.AgentID, Root: root,
		})
	}
	if toolFailed(in) {
		return
	}
	for _, t := range in.Targets() {
		_ = store.Append(event.Event{
			TS: now, Kind: event.KindWrite, Owner: event.OwnerAgent,
			Session: in.SessionID, Agent: in.AgentID, Root: t.Root,
			Dir: t.Dir, Path: t.Path, Tool: in.ToolName, Note: in.label(),
		})
	}
	if st, err := store.Load(); err == nil {
		_ = store.MaybeCompact(st)
	}
}

func toolFailed(in *Input) bool {
	if len(in.ToolResponse) == 0 {
		return false
	}
	var probe struct {
		Success *bool  `json:"success"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(in.ToolResponse, &probe) != nil {
		return false
	}
	return (probe.Success != nil && !*probe.Success) || probe.Error != ""
}

func SessionStart(in *Input, now time.Time) {
	_ = store.Append(event.Event{
		TS: now, Kind: event.KindSessionStart, Owner: event.OwnerAgent,
		Session: in.SessionID, Agent: in.AgentID,
		Root: rootOf(in.CWD), Note: in.label(),
	})
}

func SessionEnd(in *Input, now time.Time) {
	_ = store.Append(event.Event{
		TS: now, Kind: event.KindSessionEnd, Owner: event.OwnerAgent,
		Session: in.SessionID, Agent: in.AgentID,
		Root: rootOf(in.CWD), Note: in.Reason,
	})
}

func EmitContext(w io.Writer, eventName, context string) {
	if context == "" {
		return
	}
	out := map[string]any{
		"hookSpecificOutput": map[string]string{
			"hookEventName":     eventName,
			"additionalContext": context,
		},
	}
	_ = json.NewEncoder(w).Encode(out)
}

// ResolveDir maps a directory to its repository root and subtree key, for the CLI.
func ResolveDir(path string) Target { return resolve(path, path) }

// rootOf maps a session's working directory to the same repository key that write
// targets resolve to, symlinks included.
func rootOf(cwd string) string { return gitRoot(realpath(filepath.Clean(cwd))) }
