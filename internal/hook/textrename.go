package hook

import (
	"go/token"
	"path"
	"regexp"
	"slices"
	"strings"
)

// renamePair is one identifier a shell command would rewrite into another.
type renamePair struct {
	old string
	new string
}

// goMention matches a command that names Go files: an extension, or a
// search tool's file type.
var goMention = regexp.MustCompile(`\.go\b|(?:--type|-t)[ =]?go\b`)

// fileFilter matches a command that narrows its files to some kind.
var fileFilter = regexp.MustCompile(`--include|-i?name\b|--glob|\s-g\s|\*\.`)

// dynamicWord matches an argument whose value the shell fills in.
var dynamicWord = regexp.MustCompile("[$`]|^\\{\\}$")

// prefixWords may stand before a program without changing which program
// runs.
var prefixWords = []string{"do", "then", "else", "{", "sudo", "time", "command", "nice"}

// xargsValued is the xargs flags that take a value of their own.
var xargsValued = []string{"-I", "-n", "-P", "-L", "-d", "-E", "-s", "-a"}

// textRenames returns the identifier renames a shell command would make
// across Go files by rewriting text: an in-place sed or perl substitution,
// or a gofmt rewrite rule, whose pattern and replacement are both single Go
// identifiers and whose files are Go files. A command it cannot read all
// the way is not a rename.
func textRenames(command string) []renamePair {
	var out []renamePair

	segments, ok := shellSegments(command)
	if !ok {
		return nil
	}

	for _, words := range segments {
		inv, ok := invocationOf(words)
		if !ok {
			continue
		}

		pairs, files, ok := inv.parse()
		if !ok || !goTarget(files, inv.fed, inv.self, command) {
			continue
		}

		for _, pair := range pairs {
			if !slices.Contains(out, pair) {
				out = append(out, pair)
			}
		}
	}

	return out
}

// invocation is one program with its arguments, found past whatever a
// shell puts in front of it.
type invocation struct {
	prog string
	args []string
	// fed is true when the files come from somewhere other than the
	// arguments: xargs, or find's -exec.
	fed bool
	// self is true for a program that reads only Go files.
	self bool
}

func invocationOf(words []string) (invocation, bool) {
	var inv invocation

	words = unwrap(words, &inv)
	if len(words) == 0 {
		return inv, false
	}

	inv.prog, inv.args = path.Base(words[0]), words[1:]

	if inv.prog == "find" {
		at := slices.IndexFunc(inv.args, func(w string) bool { return w == "-exec" || w == "-execdir" })
		if at < 0 || at+1 >= len(inv.args) {
			return inv, false
		}

		rest := inv.args[at+1:]
		if end := slices.IndexFunc(rest, func(w string) bool { return w == ";" || w == "+" }); end >= 0 {
			rest = rest[:end]
		}

		inv.prog, inv.args, inv.fed = path.Base(rest[0]), rest[1:], true
	}

	return inv, true
}

// unwrap drops environment assignments, shell keywords, and an xargs with
// its flags from the front of a command.
func unwrap(words []string, inv *invocation) []string {
	for len(words) > 0 {
		word := words[0]

		switch {
		case slices.Contains(prefixWords, word), isAssignment(word):
			words = words[1:]
		case path.Base(word) == "xargs":
			inv.fed = true
			words = words[1:]

			for len(words) > 0 && strings.HasPrefix(words[0], "-") {
				if slices.Contains(xargsValued, words[0]) && len(words) > 1 {
					words = words[1:]
				}

				words = words[1:]
			}
		default:
			return words
		}
	}

	return nil
}

func isAssignment(word string) bool {
	name, _, found := strings.Cut(word, "=")

	return found && token.IsIdentifier(name)
}

// parse reads the invocation as one of the text tools and returns the
// identifier pairs its scripts rewrite and the files it names.
func (inv *invocation) parse() ([]renamePair, []string, bool) {
	switch inv.prog {
	case "sed", "gsed":
		return inv.script("")
	case "perl":
		return inv.script("p")
	case "gofmt":
		return inv.gofmt()
	default:
		return nil, nil, false
	}
}

// script reads sed's or perl's arguments. Both write in place under -i and
// take their script from -e; sed also takes it as the first bare argument.
// The needs argument names the one-letter flags that must also be present,
// which for perl is the -p that makes it print each line.
func (inv *invocation) script(needs string) ([]renamePair, []string, bool) {
	var (
		flags flagSet
		files []string
	)

	for i := 0; i < len(inv.args); i++ {
		arg := inv.args[i]

		switch {
		case arg == "":
			// BSD sed's empty backup suffix: -i ''.
		case strings.HasPrefix(arg, "--"):
			flags.long(arg)
		case strings.HasPrefix(arg, "-") && len(arg) > 1:
			if flags.cluster(arg[1:]) && i+1 < len(inv.args) {
				i++
				flags.scripts = append(flags.scripts, inv.args[i])
			}
		case len(flags.scripts) == 0 && needs == "":
			flags.scripts = append(flags.scripts, arg)
		default:
			files = append(files, arg)
		}
	}

	if !flags.inPlace || flags.fromFile || !containsAll(flags.letters, needs) {
		return nil, nil, false
	}

	pairs, ok := flags.pairs()

	return pairs, files, ok
}

// flagSet is what the one-letter flags of sed or perl said.
type flagSet struct {
	letters string
	scripts []string
	inPlace bool
	// fromFile is true when the script is in a file, which is not read.
	fromFile bool
}

// long reads one double-dash option.
func (f *flagSet) long(arg string) {
	if strings.HasPrefix(arg, "--in-place") {
		f.inPlace = true
	}

	if script, ok := strings.CutPrefix(arg, "--expression="); ok {
		f.scripts = append(f.scripts, script)
	}
}

// pairs reads every script the flags named, and reports false when one of
// them is more than substitutions or none of them renames an identifier.
func (f *flagSet) pairs() ([]renamePair, bool) {
	var pairs []renamePair

	for _, script := range f.scripts {
		found, ok := substitutions(script)
		if !ok {
			return nil, false
		}

		pairs = append(pairs, found...)
	}

	return pairs, len(pairs) > 0
}

// cluster reads one group of one-letter flags, such as "pi" or "Ei.bak",
// and reports whether it ended in an -e whose script is the next argument.
func (f *flagSet) cluster(letters string) bool {
	for i, r := range letters {
		switch r {
		case 'i':
			// Whatever follows -i is its backup suffix.
			f.inPlace = true

			return false
		case 'e':
			if rest := letters[i+1:]; rest != "" {
				f.scripts = append(f.scripts, rest)

				return false
			}

			return true
		case 'f':
			f.fromFile = true
		default:
			f.letters += string(r)
		}
	}

	return false
}

func containsAll(letters, needs string) bool {
	for _, r := range needs {
		if !strings.ContainsRune(letters, r) {
			return false
		}
	}

	return true
}

// gofmt reads `gofmt -r 'Old -> New' -w`, which rewrites only Go files.
func (inv *invocation) gofmt() ([]renamePair, []string, bool) {
	var (
		rule  string
		write bool
	)

	for i, arg := range inv.args {
		switch {
		case arg == "-w":
			write = true
		case arg == "-r" && i+1 < len(inv.args):
			rule = inv.args[i+1]
		}
	}

	before, after, found := strings.Cut(rule, "->")
	if !write || !found {
		return nil, nil, false
	}

	pair, ok := identifiers(strings.TrimSpace(before), strings.TrimSpace(after))
	if !ok {
		return nil, nil, false
	}

	inv.self = true

	return []renamePair{pair}, nil, true
}

// substitutions reads a script made of nothing but s commands and returns
// the ones that rewrite one identifier into another. A script holding
// anything else, an address included, is not read at all.
func substitutions(script string) ([]renamePair, bool) {
	var pairs []renamePair

	rest := strings.TrimSpace(script)
	for rest != "" {
		if len(rest) < len("s///") || rest[0] != 's' || !strings.ContainsRune("/|#,@:!%~", rune(rest[1])) {
			return nil, false
		}

		delim := rest[1]

		pattern, after, ok := untilDelim(rest[2:], delim)
		if !ok {
			return nil, false
		}

		replacement, after, ok := untilDelim(after, delim)
		if !ok {
			return nil, false
		}

		after = strings.TrimLeft(after, "gGiIp0123456789")
		if after != "" && !strings.ContainsRune(";\n", rune(after[0])) {
			return nil, false
		}

		if pair, ok := identifiers(pattern, replacement); ok {
			pairs = append(pairs, pair)
		}

		rest = strings.TrimLeft(after, "; \t\n")
	}

	return pairs, true
}

// untilDelim returns text up to the first delimiter no backslash escapes,
// and what follows it.
func untilDelim(text string, delim byte) (string, string, bool) {
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '\\':
			i++
		case delim:
			return text[:i], text[i+1:], true
		}
	}

	return "", "", false
}

// The word-boundary marks a pattern may wrap its identifier in.
var boundaries = [][2]string{{`\b`, `\b`}, {`\<`, `\>`}}

// identifiers reduces a pattern and its replacement to the two names they
// rewrite, when that is all they do: the pattern may carry word boundaries,
// and both sides may carry the same package qualifier or the same opening
// parenthesis.
func identifiers(pattern, replacement string) (renamePair, bool) {
	for _, mark := range boundaries {
		if inner, ok := strings.CutPrefix(pattern, mark[0]); ok {
			pattern = inner
		}

		if inner, ok := strings.CutSuffix(pattern, mark[1]); ok {
			pattern = inner
		}
	}

	for _, call := range []string{`\(`, `(`} {
		inner, ok := strings.CutSuffix(pattern, call)
		if !ok {
			continue
		}

		if replacement, ok = strings.CutSuffix(replacement, "("); !ok {
			return renamePair{}, false
		}

		pattern = inner

		break
	}

	if qualifier, name, ok := strings.Cut(replacement, "."); ok {
		inner, found := strings.CutPrefix(strings.Replace(pattern, `\.`, ".", 1), qualifier+".")
		if !found {
			return renamePair{}, false
		}

		pattern, replacement = inner, name
	}

	if !token.IsIdentifier(pattern) || !token.IsIdentifier(replacement) || pattern == replacement {
		return renamePair{}, false
	}

	return renamePair{old: pattern, new: replacement}, true
}

// goTarget reports whether a text tool's files are Go files. Files named
// outright decide it by their names. Files the shell fills in, from xargs,
// find, a variable, or a substitution, count when the command mentions Go
// files or narrows its files to no kind at all.
func goTarget(files []string, fed, self bool, command string) bool {
	if self {
		return true
	}

	dynamic := fed

	for _, file := range files {
		switch {
		case dynamicWord.MatchString(file):
			dynamic = true
		case strings.Contains(file, ".go"):
			return true
		}
	}

	return dynamic && (goMention.MatchString(command) || !fileFilter.MatchString(command))
}
