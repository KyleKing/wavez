// Package bashwrite recognizes shell commands that modify the working tree.
//
// Detection is heuristic. It exists because Edit and Write are not the only way an
// agent changes files: redirects, in-place editors, formatters, and codegen all bypass
// the file tools and therefore bypass any hook matched only to them. False negatives
// are expected and preferred over false positives, since a wrong hit attributes a
// subtree to a session that never touched it.
package bashwrite

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Result struct {
	IsWrite bool
	Reason  string
	Paths   []string
}

var (
	reRedirect = regexp.MustCompile(`(?:^|[^0-9&>])>>?\s*("[^"]+"|'[^']+'|[^\s;|&<>()]+)`)
	reTee      = regexp.MustCompile(`\btee\s+(?:-a\s+)?("[^"]+"|'[^']+'|[^\s;|&<>()]+)`)
	reInPlace  = regexp.MustCompile(`\b(sed|perl|ruby)\b[^;|&]*?\s-i(?:\.[A-Za-z0-9]+)?(?:\s|$)`)
	reMoveCopy = regexp.MustCompile(`\b(mv|cp|install|rsync)\s+`)
	reRemove   = regexp.MustCompile(`\b(rm|rmdir|unlink|shred)\s+`)
	reTruncate = regexp.MustCompile(`\b(truncate|dd)\b`)
	reFlagArg  = regexp.MustCompile(`^-`)
)

// formatters rewrite files in place, either always or behind the listed flag.
var formatters = map[string][]string{
	"gofmt":     {"-w"},
	"goimports": {"-w"},
	"go":        {"fmt", "generate"},
	"ruff":      {"format", "--fix"},
	"prettier":  {"--write", "-w"},
	"eslint":    {"--fix"},
	"biome":     {"--write"},
	"black":     nil,
	"isort":     nil,
	"rustfmt":   nil,
	"cargo":     {"fmt"},
	"dprint":    {"fmt"},
	"taplo":     {"fmt"},
	"mdformat":  nil,
	"shfmt":     {"-w"},
	"stylua":    nil,
	"buf":       {"generate", "format"},
	"sqlfluff":  {"fix"},
	"terraform": {"fmt"},
}

var (
	inPlaceCmds  = map[string]bool{"sed": true, "perl": true, "ruby": true}
	moveCopyCmds = map[string]bool{"mv": true, "cp": true, "install": true, "rsync": true}
	removeCmds   = map[string]bool{"rm": true, "rmdir": true, "unlink": true, "shred": true}
)

func formatterNames() map[string]bool {
	out := make(map[string]bool, len(formatters))
	for k := range formatters {
		out[k] = true
	}
	return out
}

// gitValueFlags take a separate argument, so the token after them is not the subcommand.
var gitValueFlags = map[string]bool{
	"-C": true, "-c": true, "--git-dir": true, "--work-tree": true,
	"--namespace": true, "--exec-path": true,
}

// IsGitCommit finds a git commit invocation by tokenizing rather than matching, because
// global flags with values (git -C path commit) sit between the two words.
func IsGitCommit(command string) bool {
	fields := strings.Fields(command)
	for i, f := range fields {
		if base(f) != "git" {
			continue
		}
		for j := i + 1; j < len(fields); j++ {
			tok := fields[j]
			if gitValueFlags[tok] {
				j++
				continue
			}
			if strings.HasPrefix(tok, "-") {
				continue
			}
			return tok == "commit"
		}
	}
	return false
}

func Detect(command string) Result {
	if command == "" {
		return Result{}
	}
	var paths []string
	var reasons []string

	for _, m := range reRedirect.FindAllStringSubmatch(command, -1) {
		if p := clean(m[1]); p != "" && looksLikePath(p) {
			paths = append(paths, p)
			reasons = append(reasons, "redirect")
		}
	}
	for _, m := range reTee.FindAllStringSubmatch(command, -1) {
		if p := clean(m[1]); p != "" && looksLikePath(p) {
			paths = append(paths, p)
			reasons = append(reasons, "tee")
		}
	}
	if reInPlace.MatchString(command) {
		reasons = append(reasons, "in-place edit")
		paths = append(paths, argsAfter(command, inPlaceCmds)...)
	}
	if f := matchFormatter(command); f != "" {
		reasons = append(reasons, "formatter/codegen: "+f)
		paths = append(paths, argsAfter(command, formatterNames())...)
	}
	if reMoveCopy.MatchString(command) {
		reasons = append(reasons, "move/copy")
		paths = append(paths, argsAfter(command, moveCopyCmds)...)
	}
	if reRemove.MatchString(command) {
		reasons = append(reasons, "remove")
		paths = append(paths, argsAfter(command, removeCmds)...)
	}
	if reTruncate.MatchString(command) {
		reasons = append(reasons, "truncate")
	}

	if len(reasons) == 0 {
		return Result{}
	}
	return Result{IsWrite: true, Reason: strings.Join(dedupe(reasons), ", "), Paths: dedupe(paths)}
}

func matchFormatter(command string) string {
	fields := strings.Fields(command)
	for i, f := range fields {
		name := base(f)
		flags, ok := formatters[name]
		if !ok {
			continue
		}
		if flags == nil {
			return name
		}
		for _, want := range flags {
			for _, rest := range fields[i+1:] {
				if rest == want || strings.HasPrefix(rest, want+"=") {
					return name + " " + want
				}
			}
		}
	}
	return ""
}

// argsAfter collects path-like arguments belonging to the named commands only. Scanning
// a whole segment instead pulls words out of quoted commit messages and flag values.
func argsAfter(command string, names map[string]bool) []string {
	var out []string
	for _, seg := range strings.FieldsFunc(command, func(r rune) bool {
		return r == ';' || r == '|' || r == '&' || r == '\n'
	}) {
		fields := strings.Fields(seg)
		start := -1
		for i, f := range fields {
			if names[base(f)] {
				start = i + 1
				break
			}
		}
		if start < 0 {
			continue
		}
		for _, f := range fields[start:] {
			if reFlagArg.MatchString(f) {
				continue
			}
			if p := clean(f); p != "" && looksLikePath(p) {
				out = append(out, p)
			}
		}
	}
	return out
}

func base(f string) string {
	if idx := strings.LastIndex(f, "/"); idx >= 0 {
		return f[idx+1:]
	}
	return f
}

var reExtension = regexp.MustCompile(`\.[A-Za-z0-9]{1,6}$`)

func looksLikePath(p string) bool {
	if strings.HasSuffix(p, ":") || strings.HasSuffix(p, ",") {
		return false
	}
	if p == "/" || p == "." || p == ".." {
		return false
	}
	return strings.Contains(p, "/") || reExtension.MatchString(p)
}

func clean(p string) string {
	p = strings.Trim(p, `"'`+"`")
	switch {
	case p == "", p == "-":
		return ""
	case strings.HasPrefix(p, "/dev/"):
		return ""
	case strings.Contains(p, "://"):
		return ""
	case strings.HasPrefix(p, "$"), strings.HasPrefix(p, "%"):
		return ""
	case strings.ContainsAny(p, "*?{}()<>"):
		return ""
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
