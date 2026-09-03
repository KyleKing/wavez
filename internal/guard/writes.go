package guard

import (
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// Write targets a shell command modifies, recognized from the command text.
// Redirects, in-place editors, formatters, and codegen all change files
// without going through the edit tools, so a lease taken only where those
// tools write covers about four fifths of the writes an agent makes
// (measured in _ai_/notes/agent-lock-coordination.md).
//
// Detection is heuristic and errs toward missing a write rather than
// inventing one, because a wrong hit makes a thread wait on a subtree it
// never touches.
var (
	reRedirect = regexp.MustCompile(`(?:^|[^0-9&>])>>?\s*("[^"]+"|'[^']+'|[^\s;|&<>()]+)`)
	reTee      = regexp.MustCompile(`\btee\s+(?:-a\s+)?("[^"]+"|'[^']+'|[^\s;|&<>()]+)`)
	reInPlace  = regexp.MustCompile(`\b(sed|perl|ruby)\b[^;|&]*?\s-i(?:\.[A-Za-z0-9]+)?(?:\s|$)`)
)

var (
	inPlaceCommands  = map[string]bool{"perl": true, "ruby": true, "sed": true}
	moveCopyCommands = map[string]bool{"cp": true, subInstall: true, "mv": true, "rsync": true}
	removeCommands   = map[string]bool{"rm": true, "rmdir": true, "shred": true, "unlink": true}
	teeCommands      = map[string]bool{cmdTee: true}
)

// formatters rewrite files in place, either always or behind one of the
// listed subcommands or flags.
var formatters = map[string][]string{
	"biome":     {"--write"},
	"black":     nil,
	"cargo":     {subFmt},
	"go":        {subFmt, "generate"},
	cmdGofmt:    {flagWrite},
	"gofumpt":   {flagWrite},
	"goimports": {flagWrite},
	"isort":     nil,
	"mdformat":  nil,
	"prettier":  {"--write", flagWrite},
	"ruff":      {"format", "--fix"},
	"shfmt":     {flagWrite},
	"taplo":     {subFmt},
}

const (
	flagWrite = "-w"
	subFmt    = "fmt"
)

// WriteTargets returns the paths command would modify, cleaned and relative
// to the project root where they fall inside it. An empty result means the
// command was not recognized as writing.
func WriteTargets(command string, env Env) []string {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return nil
	}

	root := cleanRoot(env.ProjectRoot)

	if p := walk(trimmed); p != nil {
		return normalizeTargets(parsedTargets(p), root)
	}

	// The parser rejected the text, so the regexps answer. They read the whole
	// line at once, so a target named in one command reaches the rules of
	// another, which errs toward leasing more than the command writes.
	return normalizeTargets(textTargets(trimmed), root)
}

// parsedTargets collects what every command writes, each judged against its
// own words. A substitution runs its commands too, so its writes count.
func parsedTargets(p *parsed) []string {
	var targets []string

	for _, pipe := range p.top {
		for _, stage := range pipe.stages {
			targets = append(targets, stageTargets(stage.words)...)
		}
	}

	for _, sub := range p.subs {
		for _, pipe := range sub.top {
			for _, stage := range pipe.stages {
				targets = append(targets, stageTargets(stage.words)...)
			}
		}
	}

	return targets
}

// stageTargets reads one command's words for the files it rewrites.
func stageTargets(words []string) []string {
	targets := redirectTargets(words)

	if hasInPlaceFlag(words) {
		targets = append(targets, wordsAfter(words, inPlaceCommands)...)
	}

	if stageFormats(words) {
		targets = append(targets, wordsAfter(words, formatterNames())...)
	}

	for _, names := range []map[string]bool{moveCopyCommands, removeCommands, teeCommands} {
		targets = append(targets, wordsAfter(words, names)...)
	}

	return targets
}

// redirectTargets reads the file each output redirection names. A duplication
// (`2>&1`) names a descriptor rather than a file, so it is skipped.
func redirectTargets(words []string) []string {
	var out []string

	for i, word := range words {
		if !isWriteRedirect(word) || i+1 >= len(words) {
			continue
		}

		target := words[i+1]
		if strings.Contains(word, "&") && isDescriptor(target) {
			continue
		}

		out = append(out, target)
	}

	return out
}

// isWriteRedirect reports whether word is a redirection operator that opens
// its target for writing, with any leading file descriptor stripped.
func isWriteRedirect(word string) bool {
	op := strings.TrimLeft(word, "0123456789")

	switch op {
	case ">", ">>", ">|", ">&", ">>&", "&>", "&>>", "<>":
		return true
	default:
		return false
	}
}

func isDescriptor(word string) bool {
	if word == "-" {
		return true
	}

	return word != "" && strings.TrimLeft(word, "0123456789") == ""
}

func hasInPlaceFlag(words []string) bool {
	if !holdsCommand(words, inPlaceCommands) {
		return false
	}

	for _, word := range words {
		if word == "-i" || strings.HasPrefix(word, "-i.") {
			return true
		}
	}

	return false
}

// stageFormats reports whether this command rewrites the files it is given,
// either always or behind one of the subcommands or flags it is listed with.
func stageFormats(words []string) bool {
	for _, word := range words {
		flags, ok := formatters[baseName(word)]
		if !ok {
			continue
		}

		if flags == nil {
			return true
		}

		for _, flag := range flags {
			if slices.Contains(words, flag) {
				return true
			}
		}
	}

	return false
}

func holdsCommand(words []string, names map[string]bool) bool {
	for _, word := range words {
		if names[baseName(word)] {
			return true
		}
	}

	return false
}

// wordsAfter collects the non-flag words following any of names, which is
// where a command of this shape puts the files it rewrites.
func wordsAfter(words []string, names map[string]bool) []string {
	var out []string

	collecting := false

	for _, word := range words {
		switch {
		case names[baseName(word)]:
			collecting = true
		case !collecting:
		case strings.HasPrefix(word, "-"):
		case isWriteRedirect(word):
			collecting = false
		default:
			out = append(out, word)
		}
	}

	return out
}

// textTargets is the pre-parser reading, kept for text bash rejects.
func textTargets(command string) []string {
	var targets []string

	for _, m := range reRedirect.FindAllStringSubmatch(command, -1) {
		targets = append(targets, m[1])
	}

	for _, m := range reTee.FindAllStringSubmatch(command, -1) {
		targets = append(targets, m[1])
	}

	if reInPlace.MatchString(command) {
		targets = append(targets, argsAfter(command, inPlaceCommands)...)
	}

	if matchesFormatter(command) {
		targets = append(targets, argsAfter(command, formatterNames())...)
	}

	for _, names := range []map[string]bool{moveCopyCommands, removeCommands} {
		if hasCommand(command, names) {
			targets = append(targets, argsAfter(command, names)...)
		}
	}

	return targets
}

func formatterNames() map[string]bool {
	out := make(map[string]bool, len(formatters))
	for name := range formatters {
		out[name] = true
	}

	return out
}

func matchesFormatter(command string) bool {
	for _, tok := range tokenize(command) {
		flags, ok := formatters[baseName(tok)]
		if !ok {
			continue
		}

		if flags == nil {
			return true
		}

		for _, flag := range flags {
			if strings.Contains(command, flag) {
				return true
			}
		}
	}

	return false
}

func hasCommand(command string, names map[string]bool) bool {
	for _, tok := range tokenize(command) {
		if names[baseName(tok)] {
			return true
		}
	}

	return false
}

// argsAfter collects the non-flag arguments following any of names, which is
// where a command of this shape puts the files it rewrites.
func argsAfter(command string, names map[string]bool) []string {
	tokens := tokenize(command)

	var out []string

	collecting := false

	for _, tok := range tokens {
		switch {
		case names[baseName(tok)]:
			collecting = true
		case !collecting:
		case strings.HasPrefix(tok, "-"):
		case isShellOperator(tok):
			collecting = false
		default:
			out = append(out, tok)
		}
	}

	return out
}

func isShellOperator(tok string) bool {
	switch tok {
	case "&&", "||", ";", "|", ">", ">>":
		return true
	default:
		return false
	}
}

// looksLikeTarget rejects what cannot name a file a lease could cover: a
// stdin dash, a device, a URL, and anything holding a character this package
// refuses to resolve. A caller with the filesystem in hand narrows the rest
// further, since the text alone cannot tell a path from a sed script.
func looksLikeTarget(path string) bool {
	switch {
	case path == "", path == "-", path == "/", path == ".", path == "..":
		return false
	case strings.HasPrefix(path, "/dev/"), strings.Contains(path, "://"):
		return false
	case strings.ContainsAny(path, unresolvedChars):
		return false
	default:
		return true
	}
}

// normalizeTargets drops anything that does not read as a path, resolves what
// is left against the root, and sorts and dedupes the result.
func normalizeTargets(targets []string, root string) []string {
	seen := map[string]bool{}

	var out []string

	for _, t := range targets {
		path := strings.Trim(strings.TrimSpace(t), `"'`+"`")
		if !looksLikeTarget(path) {
			continue
		}

		if !filepath.IsAbs(path) && root != "" {
			path = filepath.Join(root, path)
		}

		path = filepath.Clean(path)
		if seen[path] {
			continue
		}

		seen[path] = true

		out = append(out, path)
	}

	sort.Strings(out)

	return out
}
