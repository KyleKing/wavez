package guard

import (
	"path/filepath"
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
	tokens := tokenize(cmd)
	if len(tokens) == 0 {
		return finding{Verdict: NeedsApproval, Reason: "empty command could not be classified", Fragment: cmd}
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
	case "git":
		candidates = append(candidates, classifyGit(cmd, tokens))
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
	if strings.HasPrefix(name, "mkfs") {
		candidates = append(candidates, finding{Verdict: Refuse, Reason: "formats a filesystem", Fragment: cmd})
	}
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

func classifyGit(cmd string, tokens []string) finding {
	if len(tokens) < minGitTokens {
		return finding{Verdict: Allow, Reason: reasonNoMatch, Fragment: cmd}
	}

	switch tokens[1] {
	case "push":
		for _, tok := range tokens[2:] {
			if tok == "--force" || tok == "-f" || tok == "--force-with-lease" ||
				strings.HasPrefix(tok, "--force-with-lease=") {
				return finding{Verdict: NeedsApproval, Reason: "force push can overwrite remote history", Fragment: cmd}
			}
		}
	case "reset":
		for _, tok := range tokens[2:] {
			if tok == "--hard" {
				return finding{
					Verdict: NeedsApproval, Reason: "git reset --hard discards uncommitted work", Fragment: cmd,
				}
			}
		}
	case "rebase", "filter-branch", "filter-repo":
		return finding{Verdict: NeedsApproval, Reason: "rewrites commit history", Fragment: cmd}
	}

	return finding{Verdict: Allow, Reason: reasonNoMatch, Fragment: cmd}
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
