package guard

import (
	"path/filepath"
	"regexp"
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
	if strings.TrimSpace(command) == "" {
		return nil
	}

	root := cleanRoot(env.ProjectRoot)

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

	return normalizeTargets(targets, root)
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
