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

// textRename is one rename a shell command would make by rewriting text,
// and what the command says about where.
type textRename struct {
	pair renamePair
	// qualifier is the package name the pattern put in front of the
	// identifier, as in `bench\.Read`, or "".
	qualifier string
	// dir is where `cd` had taken the shell by the time the command ran,
	// relative to where it started, or "".
	dir string
	// files are the arguments that name Go files outright: a path ending
	// in .go, or a glob that could match one.
	files []string
	// module is true when the files are not named but are Go files all the
	// same: fed by a pipeline that filters on *.go, or gofmt's own.
	module bool
}

// maxShellDepth is how many `sh -c` wrappers deep a command is read.
const maxShellDepth = 2

var (
	// The dynamicWord pattern matches an argument whose value the shell fills in.
	dynamicWord = regexp.MustCompile("[$`]|^\\{\\}$")
	// The goGlob pattern matches a word that filters on Go source: `*.go` at the end of a
	// name, a glob, or an --include.
	goGlob = regexp.MustCompile(`\*\.go(?:$|[^A-Za-z0-9_.])`)
	// The globChars are what make a word a pattern instead of a path.
	globChars = "*?["
)

// prefixWords may stand before a program without changing which program
// runs.
var prefixWords = []string{"do", "then", "else", "{", "sudo", "time", "command", "nice"}

// xargsValued is the xargs flags that take a value of their own.
var xargsValued = []string{"-I", "-n", "-P", "-L", "-d", "-E", "-s", "-a"}

// shells are the programs whose -c argument is a command line of its own.
var shells = []string{"sh", "bash", "zsh", "dash", "ksh"}

// textRenames returns the identifier renames a shell command would make
// across Go files by rewriting text: an in-place sed or perl substitution,
// or a gofmt rewrite rule, whose pattern and replacement are both single Go
// identifiers and whose files are Go files. The files count as Go files
// when an argument names one, or when the command feeds files it filtered
// on `*.go`. A command it cannot read all the way is not a rename, and
// neither is one whose files it cannot tell are Go: a wrong denial costs
// the agent a turn and a missed one costs nothing new.
func textRenames(command string) []textRename {
	return renamesAt(command, 0)
}

func renamesAt(command string, depth int) []textRename {
	segments, ok := shellSegments(command)
	if !ok {
		return nil
	}

	var (
		out []textRename
		dir string
	)

	fedGo := slices.ContainsFunc(segments, namesGoFiles)

	for _, words := range segments {
		if words[0] == "cd" {
			var ok bool

			// Where the shell is after a cd nobody can follow is unknown,
			// and so is what a relative path after it names.
			if dir, ok = changedDir(dir, words); !ok {
				return out
			}

			continue
		}

		inv, ok := invocationOf(words)
		if !ok {
			continue
		}

		found := inv.renames(fedGo)
		if script, ok := inv.shellScript(); ok && depth < maxShellDepth {
			found = renamesAt(script, depth+1)
		}

		for _, rename := range found {
			rename.dir = path.Join(dir, rename.dir)

			if !slices.ContainsFunc(out, func(seen textRename) bool { return seen.pair == rename.pair }) {
				out = append(out, rename)
			}
		}
	}

	return out
}

// changedDir reads `cd <literal directory>` and returns where it leaves a
// shell that was in dir.
func changedDir(dir string, words []string) (string, bool) {
	if len(words) != 2 || dynamicWord.MatchString(words[1]) || strings.HasPrefix(words[1], "-") ||
		strings.HasPrefix(words[1], "~") {
		return "", false
	}

	if path.IsAbs(words[1]) {
		return words[1], true
	}

	return path.Join(dir, words[1]), true
}

// namesGoFiles reports whether a command filters its files on Go source: a
// `*.go` pattern anywhere in it, or a search tool's Go file type.
func namesGoFiles(words []string) bool {
	for i, word := range words {
		switch {
		case goGlob.MatchString(word), word == "-tgo", word == "--type=go":
			return true
		case (word == "-t" || word == "--type") && i+1 < len(words) && words[i+1] == "go":
			return true
		}
	}

	return false
}

// invocation is one program with its arguments, found past whatever a
// shell puts in front of it.
type invocation struct {
	prog string
	args []string
	// fed is true when the files come from somewhere other than the
	// arguments: xargs, or find's -exec.
	fed bool
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
		if at < 0 {
			return inv, false
		}

		rest := inv.args[at+1:]
		if end := slices.IndexFunc(rest, func(w string) bool { return w == ";" || w == "+" }); end >= 0 {
			rest = rest[:end]
		}

		// `-exec` with nothing after it, or with only its terminator, runs
		// no program.
		if len(rest) == 0 {
			return inv, false
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

// shellScript returns the command line a `sh -c '...'` runs.
func (inv *invocation) shellScript() (string, bool) {
	if !slices.Contains(shells, inv.prog) {
		return "", false
	}

	for i, arg := range inv.args {
		if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.HasSuffix(arg, "c") &&
			i+1 < len(inv.args) {
			return inv.args[i+1], true
		}
	}

	return "", false
}

// renames reads the invocation as one of the text tools and returns the
// renames it would make. The fedGo argument says whether the command line
// it is part of filters files on Go source.
func (inv *invocation) renames(fedGo bool) []textRename {
	var (
		found []textRename
		args  []string
		ok    bool
	)

	switch inv.prog {
	case "sed", "gsed":
		found, args, ok = inv.script(sedFlags)
	case "perl":
		found, args, ok = inv.script(perlFlags)
	case "gofmt":
		// gofmt reads nothing but Go files, whatever it is pointed at.
		found, _ = inv.gofmt()
		for i := range found {
			found[i].module = true
		}

		return found
	}

	if !ok {
		return nil
	}

	files, dynamic := goArguments(args)
	module := (dynamic || inv.fed) && fedGo

	if len(files) == 0 && !module {
		return nil
	}

	for i := range found {
		found[i].files, found[i].module = files, module && len(files) == 0
	}

	return found
}

// goArguments picks the file arguments that name Go files: a path ending
// in .go, or a glob that ends in .go or in a star. It also reports whether
// any argument is one the shell fills in.
func goArguments(args []string) ([]string, bool) {
	var (
		files   []string
		dynamic bool
	)

	for _, arg := range args {
		switch {
		case dynamicWord.MatchString(arg):
			dynamic = true
		case strings.HasSuffix(arg, ".go"):
			files = append(files, arg)
		case strings.ContainsAny(arg, globChars) && strings.HasSuffix(arg, "*"):
			files = append(files, arg)
		}
	}

	return files, dynamic
}

// flagRules says how one program reads its one-letter flags.
type flagRules struct {
	// plain are the flags that take no value.
	plain string
	// valued are the flags whose value is the rest of the cluster, or the
	// next argument when the cluster ends there.
	valued string
	// attached are the flags whose value is always the rest of the cluster.
	attached string
	// needs are the plain flags that must be present.
	needs string
	// bare is true when the first argument that is not a flag is the
	// script.
	bare bool
}

var (
	sedFlags = flagRules{plain: "Enrsuz", valued: "l", bare: true}
	// Perl's -M, -m, -I and their like swallow the rest of their argument,
	// which is how `-MFile::Slurp` holds an i and is not -i.
	perlFlags = flagRules{plain: "pnlawsTtUuWXc", attached: "MmIFCxdDV0", needs: "p"}
)

// script reads sed's or perl's arguments. Both write in place under -i and
// take their script from -e; sed also takes it as the first bare argument.
// A flag the rules do not know means the command was not understood.
func (inv *invocation) script(rules flagRules) ([]textRename, []string, bool) {
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
			used, ok := flags.flag(arg[1:], inv.args[i+1:], rules)
			if !ok {
				return nil, nil, false
			}

			i += used
		case len(flags.scripts) == 0 && rules.bare:
			flags.scripts = append(flags.scripts, arg)
		default:
			files = append(files, arg)
		}
	}

	if !flags.inPlace || flags.fromFile || !containsAll(flags.letters, rules.needs) {
		return nil, nil, false
	}

	found, ok := flags.renames()

	return found, files, ok
}

// flagSet is what the one-letter flags of sed or perl said.
type flagSet struct {
	letters string
	scripts []string
	inPlace bool
	// fromFile is true when the script is in a file, which is not read.
	fromFile bool
}

// What the argument after a cluster of flags is.
const (
	nextIsFile = iota
	nextIsScript
	nextIsValue
)

// flag reads one group of one-letter flags and, when the group ends in a
// flag that takes the next argument, that argument too. It returns how many
// of rest it used, and false for a flag the rules do not know.
func (f *flagSet) flag(letters string, rest []string, rules flagRules) (int, bool) {
	next, ok := f.cluster(letters, rules)

	switch {
	case !ok:
		return 0, false
	case next == nextIsFile || len(rest) == 0:
		return 0, true
	case next == nextIsScript:
		f.scripts = append(f.scripts, rest[0])
	}

	return 1, true
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

// renames reads every script the flags named, and reports false when one of
// them is more than substitutions or none of them renames an identifier.
func (f *flagSet) renames() ([]textRename, bool) {
	var found []textRename

	for _, script := range f.scripts {
		some, ok := substitutions(script)
		if !ok {
			return nil, false
		}

		found = append(found, some...)
	}

	return found, len(found) > 0
}

// cluster reads one group of one-letter flags, such as "pi" or "Ei.bak".
// It says what the next argument is, and false for a flag it does not know.
func (f *flagSet) cluster(letters string, rules flagRules) (int, bool) {
	for i, r := range letters {
		rest := letters[i+1:]

		switch {
		case r == 'i':
			// Whatever follows -i is its backup suffix.
			f.inPlace = true

			return nextIsFile, true
		case r == 'e':
			if rest != "" {
				f.scripts = append(f.scripts, rest)

				return nextIsFile, true
			}

			return nextIsScript, true
		case r == 'f':
			f.fromFile = true

			return nextIsFile, true
		case strings.ContainsRune(rules.attached, r):
			return nextIsFile, true
		case strings.ContainsRune(rules.valued, r):
			if rest != "" {
				return nextIsFile, true
			}

			return nextIsValue, true
		case strings.ContainsRune(rules.plain, r):
			f.letters += string(r)
		default:
			return nextIsFile, false
		}
	}

	return nextIsFile, true
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
func (inv *invocation) gofmt() ([]textRename, bool) {
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
		return nil, false
	}

	rename, ok := identifiers(strings.TrimSpace(before), strings.TrimSpace(after))
	if !ok {
		return nil, false
	}

	return []textRename{rename}, true
}

// substitutions reads a script made of nothing but s commands and returns
// the ones that rewrite one identifier into another. A script holding
// anything else, an address included, is not read at all.
func substitutions(script string) ([]textRename, bool) {
	var found []textRename

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

		if rename, ok := identifiers(pattern, replacement); ok {
			found = append(found, rename)
		}

		rest = strings.TrimLeft(after, "; \t\n")
	}

	return found, true
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
func identifiers(pattern, replacement string) (textRename, bool) {
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
			return textRename{}, false
		}

		pattern = inner

		break
	}

	var rename textRename

	if qualifier, name, ok := strings.Cut(replacement, "."); ok {
		inner, found := strings.CutPrefix(strings.Replace(pattern, `\.`, ".", 1), qualifier+".")
		if !found || !token.IsIdentifier(qualifier) {
			return textRename{}, false
		}

		pattern, replacement, rename.qualifier = inner, name, qualifier
	}

	if !token.IsIdentifier(pattern) || !token.IsIdentifier(replacement) || pattern == replacement {
		return textRename{}, false
	}

	rename.pair = renamePair{old: pattern, new: replacement}

	return rename, true
}
