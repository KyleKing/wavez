package guard

import (
	"path/filepath"
	"slices"
	"strings"
)

var shellInterpreters = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true,
}

var fetchCommands = map[string]bool{
	"curl": true, "wget": true,
}

var diskutilDestructive = map[string]bool{
	"eraseDisk": true, "eraseVolume": true, "zeroDisk": true,
	"partitionDisk": true, "eraseAllDisk": true,
}

const minPipelineStages = 2

const minGitTokens = 2

// cmdGit and subShow name literals several rules share.
const (
	cmdGit  = "git"
	subShow = "show"
)

// cmdDiff, cmdGofmt, and cmdMise name literals the allowlist, the
// already-answered checks, and the write-target rules each repeat.
const (
	cmdDiff  = "diff"
	cmdGofmt = "gofmt"
	cmdMise  = "mise"
)

// propRestore is the subcommand name git and jj share.
const propRestore = "restore"

// reasonForcePush is a refusal rather than an approval prompt. Overwriting
// published history is not recoverable from this side of the remote, and an
// approval prompt makes the destructive path one keystroke away from the
// ordinary one.
const reasonForcePush = "a force push overwrites history on the remote and cannot be undone from here"

// pipeToShellReason reports whether pipeline feeds a curl or wget stage into
// a shell interpreter stage, a pattern that runs arbitrary remote code.
func pipeToShellReason(pipeline string) (string, bool) {
	stages := splitPipeline(pipeline)
	if len(stages) < minPipelineStages {
		return "", false
	}

	sawFetch := false
	for _, stage := range stages {
		tokens := tokenize(stage)
		if len(tokens) == 0 {
			continue
		}
		if fetchCommands[tokens[0]] {
			sawFetch = true
			continue
		}
		if sawFetch && shellInterpreters[baseName(tokens[0])] {
			return "pipes a network fetch into a shell interpreter", true
		}
	}

	return "", false
}

func isForkBomb(cmd string) bool {
	compact := strings.Join(strings.Fields(cmd), "")
	return strings.Contains(compact, ":(){:|:&};:")
}

// classifyCommand gathers every rule that matches cmd and returns the worst
// of them: a command can trip more than one pattern (rm -rf on a .git path
// is both a scoped delete and a git-internals write), and picking only the
// first match risks under-reporting the real severity.
func classifyCommand(cmd string, env Env) finding {
	raw := tokenize(cmd)
	if len(raw) == 0 {
		return finding{Verdict: NeedsApproval, Reason: "empty command could not be classified", Fragment: cmd}
	}

	tokens := withoutAssignments(raw)
	if len(tokens) == 0 {
		return finding{Verdict: Allow, Reason: "sets a variable and runs nothing", Fragment: cmd}
	}

	var candidates []finding

	for _, tok := range tokens {
		if tok == "sudo" {
			candidates = append(candidates, finding{
				Verdict: Refuse, Reason: "sudo steps outside the sandbox's user scope", Fragment: cmd,
			})

			break
		}
	}

	name := baseName(tokens[0])
	switch name {
	case "rm":
		candidates = append(candidates, classifyRM(cmd, tokens, env))
	case cmdGit:
		candidates = append(candidates, classifyGit(cmd, tokens, env))
	case "jj":
		candidates = append(candidates, classifyJJ(cmd, tokens))
	case "chmod", "chown":
		candidates = append(candidates, classifyChmodChown(cmd, tokens, env))
	case "dd":
		candidates = append(candidates, classifyDD(cmd, tokens))
	case "diskutil":
		candidates = append(candidates, classifyDiskutil(cmd, tokens))
	case "kill":
		candidates = append(candidates, classifyKill(cmd, tokens))
	case "xargs":
		candidates = append(candidates, classifyXargs(cmd, tokens, env))
	}
	candidates = append(candidates, byName(cmd, tokens[0], name, env)...)
	if killallProcessWide(name, tokens) {
		candidates = append(candidates, finding{
			Verdict: NeedsApproval, Reason: "kills processes by name across the whole user", Fragment: cmd,
		})
	}
	if reason, hit := gitInternalsWrite(tokens); hit {
		candidates = append(candidates, finding{Verdict: Refuse, Reason: reason, Fragment: cmd})
	}

	return worst(candidates, cmd)
}

// byName gathers the rules that key off the command's own name rather than
// its arguments.
func byName(cmd, prog, name string, env Env) []finding {
	var out []finding

	if !env.allowed(prog, name) {
		out = append(out, finding{Verdict: NeedsApproval, Reason: name + reasonUnlisted, Fragment: cmd})
	}

	if instead, superseded := supersededTools[name]; superseded {
		out = append(out, finding{
			Verdict:  Refuse,
			Reason:   name + " is not available here: " + instead,
			Fragment: cmd,
		})
	}

	if strings.HasPrefix(name, "mkfs") {
		out = append(out, finding{Verdict: Refuse, Reason: "formats a filesystem", Fragment: cmd})
	}

	return out
}

// supersededTools name shell commands a built-in tool does better, and what
// to reach for instead. They are refused rather than approved because the
// point is to redirect: measured over 278 logged shell calls, around 70% of
// what the shell was used for was work a tool already did, and asking a
// model not to reach for `find` has never moved that number.
//
// `truncate` has no read-only use at all; it destroys a file's tail in
// place, which is what `write` and `str_replace` do with the content stated.
var supersededTools = map[string]string{
	"find": "list names what is under a directory (with a glob), and search reads " +
		"contents through the code index",
	"truncate": "write replaces a whole file and str_replace replaces part of one",
	// Only a version-control write reaches this. Shell answers a read-only
	// status, diff, or log from what the run recorded as it wrote, before
	// classifying anything. Refusing the rest is what keeps a force push, a
	// history rewrite, and a git commit in a jj checkout from being one
	// shell string away, and naming undo is what a run reverting its own
	// edit actually needs: one h7 lane spent 44 turns on three refused
	// attempts at `git checkout -- <file>`.
	cmdGit: "the harness owns version control; undo puts back a file this run edited",
	"jj":   "the harness owns version control; undo puts back a file this run edited",
}

// classifyXargs classifies the command xargs would invoke per input line,
// skipping xargs' own flags. Without a concrete argument (the common case,
// since xargs' targets come from stdin) rules like rm -rf fall back to
// NeedsApproval for lack of a target to check.
func classifyXargs(cmd string, tokens []string, env Env) finding {
	inner := tokens[1:]
	for len(inner) > 0 && strings.HasPrefix(inner[0], "-") {
		inner = inner[1:]
	}
	if len(inner) == 0 {
		return finding{Verdict: Allow, Reason: reasonNoMatch, Fragment: cmd}
	}

	return classifyCommand(strings.Join(inner, " "), env)
}

func classifyRM(cmd string, tokens []string, env Env) finding {
	if !hasRecursiveForce(tokens) {
		return finding{Verdict: Allow, Reason: reasonNoMatch, Fragment: cmd}
	}

	var targets []string
	for _, tok := range tokens[1:] {
		if strings.HasPrefix(tok, "-") {
			continue
		}
		targets = append(targets, tok)
	}
	if len(targets) == 0 {
		return finding{Verdict: NeedsApproval, Reason: "rm -rf with no resolvable target", Fragment: cmd}
	}

	// Expansion happens before the containment test, because an unexpanded
	// `$HOME/x` joins onto the project root and reads as inside it, which
	// is how `rm -rf $HOME/thing` came to be allowed. A target that will
	// not resolve is approval-worthy rather than allowed, for the reason
	// the package doc gives: this guard fails closed.
	for _, target := range targets {
		expanded, resolved := env.expandPath(target)
		if !resolved {
			return finding{
				Verdict:  NeedsApproval,
				Reason:   "rm -rf targets " + target + ", which does not resolve to one location",
				Fragment: cmd,
			}
		}

		if isOutsideOrAtRoot(expanded, env.ProjectRoot) {
			return finding{
				Verdict:  Refuse,
				Reason:   "rm -rf targets " + expanded + ", which is at or outside the project root",
				Fragment: cmd,
			}
		}
	}

	return finding{Verdict: Allow, Reason: "rm -rf scoped inside the project root", Fragment: cmd}
}

func hasRecursiveForce(tokens []string) bool {
	recursive, force := false, false
	for _, tok := range tokens[1:] {
		if !strings.HasPrefix(tok, "-") {
			continue
		}
		switch tok {
		case "--recursive":
			recursive = true
		case "--force":
			force = true
		default:
			if strings.HasPrefix(tok, "--") {
				continue
			}
			flags := tok[1:]
			if strings.ContainsAny(flags, "rR") {
				recursive = true
			}
			if strings.Contains(flags, "f") {
				force = true
			}
		}
	}

	return recursive && force
}

// gitReadOnly names the git subcommands that only report. In a colocated
// checkout everything else is refused rather than approved, because the
// project's rule is that jj owns writes and a git write there leaves jj
// holding a working copy that no longer matches what git did.
var gitReadOnly = map[string]bool{
	"blame": true, "cat-file": true, "config": true, "describe": true, cmdDiff: true,
	"grep": true, "log": true, "ls-files": true, "ls-remote": true, "rev-parse": true,
	"shortlog": true, subShow: true, "status": true,
}

// classifyGit grades a git command by subcommand. It sits behind the ban in
// supersededTools, which refuses every git write outright, so its grades no
// longer decide a verdict on their own. It stays because the ban is a
// policy and this is the safety analysis: narrowing the ban must not
// silently allow a history rewrite.
func classifyGit(cmd string, tokens []string, env Env) finding {
	if len(tokens) < minGitTokens {
		return finding{Verdict: Allow, Reason: reasonNoMatch, Fragment: cmd}
	}

	if env.ColocatedJJ && !gitReadOnly[tokens[1]] {
		return finding{
			Verdict:  Refuse,
			Reason:   "this project is a colocated jj checkout, where jj owns the working copy; use jj",
			Fragment: cmd,
		}
	}

	switch tokens[1] {
	case "push":
		if forcedPush(tokens[2:]) {
			return finding{Verdict: Refuse, Reason: reasonForcePush, Fragment: cmd}
		}
	case "reset":
		if slices.Contains(tokens[2:], "--hard") {
			return finding{
				Verdict: NeedsApproval, Reason: "git reset --hard discards uncommitted work", Fragment: cmd,
			}
		}
	case "rebase", "filter-branch", "filter-repo":
		return finding{Verdict: NeedsApproval, Reason: "rewrites commit history", Fragment: cmd}
	case "stash":
		return classifyGitStash(cmd, tokens)
	case "checkout", "switch", propRestore:
		return finding{
			Verdict: NeedsApproval, Reason: "replaces files in the working copy", Fragment: cmd,
		}
	case "clean":
		return finding{Verdict: NeedsApproval, Reason: "deletes untracked files", Fragment: cmd}
	case "worktree":
		return finding{
			Verdict: NeedsApproval, Reason: "adds a second working copy of this repository", Fragment: cmd,
		}
	}

	return finding{Verdict: Allow, Reason: reasonNoMatch, Fragment: cmd}
}

// classifyGitStash separates reading the stash from moving work into or out
// of it. A run that stashes takes uncommitted work out of the working copy,
// including whatever the user is editing beside it, and drop and clear
// destroy it outright with nothing to restore from.
func forcedPush(args []string) bool {
	for _, tok := range args {
		if tok == "--force" || tok == "-f" || tok == "--force-with-lease" ||
			strings.HasPrefix(tok, "--force-with-lease=") {
			return true
		}
	}

	return false
}

func classifyGitStash(cmd string, tokens []string) finding {
	sub := ""
	if len(tokens) > minGitTokens {
		sub = tokens[minGitTokens]
	}

	switch sub {
	case "list", "show":
		return finding{Verdict: Allow, Reason: reasonNoMatch, Fragment: cmd}
	case "drop", "clear":
		return finding{Verdict: Refuse, Reason: "discards stashed work irrecoverably", Fragment: cmd}
	default:
		return finding{
			Verdict: NeedsApproval, Reason: "moves uncommitted work out of the working copy", Fragment: cmd,
		}
	}
}

// classifyJJ holds the jj commands that discard work or rewind the
// repository. Every one is in the operation log and so recoverable, and
// each still throws away a state nobody asked it to.
// ClassifyJJ's counterpart for jj sits behind the same ban as classifyGit
// and stays for the same reason.
func classifyJJ(cmd string, tokens []string) finding {
	if len(tokens) < minGitTokens {
		return finding{Verdict: Allow, Reason: reasonNoMatch, Fragment: cmd}
	}

	sub := ""
	if len(tokens) > minGitTokens {
		sub = tokens[minGitTokens]
	}

	if reason, held := jjDiscards(tokens[1], sub); held {
		return finding{Verdict: NeedsApproval, Reason: reason, Fragment: cmd}
	}

	if tokens[1] == "git" && sub == "push" && forcedPush(tokens[3:]) {
		return finding{Verdict: Refuse, Reason: reasonForcePush, Fragment: cmd}
	}

	return finding{Verdict: Allow, Reason: reasonNoMatch, Fragment: cmd}
}

func jjDiscards(name, sub string) (string, bool) {
	switch name {
	case "abandon":
		return "jj abandon discards a change and its descendants", true
	case propRestore:
		return "jj restore discards working-copy changes", true
	case "undo":
		return "jj undo rewinds the last operation", true
	case "op":
		return "jj op rewinds the whole repository", sub == propRestore || sub == "undo"
	case "workspace":
		return "jj workspace forget drops a working copy", sub == "forget"
	default:
		return "", false
	}
}

func gitInternalsWrite(tokens []string) (string, bool) {
	writers := map[string]bool{"rm": true, "mv": true, "cp": true, "tee": true, "truncate": true}
	name := baseName(tokens[0])
	isWriter := writers[name]

	for i, tok := range tokens {
		if tok == ">" || tok == ">>" {
			isWriter = true
			continue
		}
		if !isWriter {
			continue
		}
		if i == 0 {
			continue
		}
		if touchesGitDir(tok) {
			return "writes directly to .git internals", true
		}
	}

	return "", false
}

func touchesGitDir(path string) bool {
	clean := filepath.ToSlash(filepath.Clean(path))
	for _, part := range strings.Split(clean, "/") {
		if part == ".git" {
			return true
		}
	}

	return false
}

func classifyChmodChown(cmd string, tokens []string, env Env) finding {
	var targets []string
	for _, tok := range tokens[1:] {
		if strings.HasPrefix(tok, "-") {
			continue
		}
		targets = append(targets, tok)
	}
	if len(targets) == 0 {
		return finding{Verdict: NeedsApproval, Reason: "chmod/chown with no resolvable target", Fragment: cmd}
	}
	target := targets[len(targets)-1]

	expanded, resolved := env.expandPath(target)
	if !resolved {
		return finding{
			Verdict:  NeedsApproval,
			Reason:   tokens[0] + " targets " + target + ", which does not resolve to one location",
			Fragment: cmd,
		}
	}

	if isOutsideOrAtRoot(expanded, env.ProjectRoot) {
		return finding{
			Verdict:  Refuse,
			Reason:   tokens[0] + " targets " + expanded + ", which is outside the project root",
			Fragment: cmd,
		}
	}

	return finding{Verdict: Allow, Reason: reasonNoMatch, Fragment: cmd}
}

func classifyDD(cmd string, tokens []string) finding {
	for _, tok := range tokens[1:] {
		if strings.HasPrefix(tok, "of=/dev/") {
			return finding{Verdict: Refuse, Reason: "dd writes directly to a block device", Fragment: cmd}
		}
	}

	return finding{Verdict: NeedsApproval, Reason: "dd can overwrite arbitrary data", Fragment: cmd}
}

func classifyDiskutil(cmd string, tokens []string) finding {
	if len(tokens) >= 2 && diskutilDestructive[tokens[1]] {
		return finding{Verdict: Refuse, Reason: "diskutil " + tokens[1] + " destroys disk contents", Fragment: cmd}
	}

	return finding{Verdict: NeedsApproval, Reason: "diskutil can modify disk partitions", Fragment: cmd}
}

func classifyKill(cmd string, tokens []string) finding {
	hasNine, hasNegOne := false, false
	for _, tok := range tokens[1:] {
		switch tok {
		case "-9":
			hasNine = true
		case "-1":
			hasNegOne = true
		}
	}
	if hasNine && hasNegOne {
		return finding{Verdict: Refuse, Reason: "kills every process the user owns", Fragment: cmd}
	}

	return finding{Verdict: Allow, Reason: reasonNoMatch, Fragment: cmd}
}

func killallProcessWide(name string, tokens []string) bool {
	if name != "killall" && name != "pkill" {
		return false
	}
	for _, tok := range tokens[1:] {
		if tok == "-9" {
			return true
		}
	}

	return false
}

func baseName(cmd string) string {
	return filepath.Base(cmd)
}

// isOutsideOrAtRoot reports whether target, resolved lexically against root
// (no filesystem access), is the project root itself, above it, or a
// well-known home-directory or filesystem-root shorthand.
func isOutsideOrAtRoot(target, root string) bool {
	if target == "/" || target == "~" || target == "$HOME" || strings.HasPrefix(target, "~/") {
		return true
	}

	var abs string
	if filepath.IsAbs(target) {
		abs = filepath.Clean(target)
	} else {
		abs = filepath.Clean(filepath.Join(root, target))
	}
	if abs == root {
		return true
	}

	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return true
	}

	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func cleanRoot(root string) string {
	if root == "" {
		return root
	}

	return filepath.Clean(root)
}
