// Package lookup answers, before the model has to ask, where the things a
// prompt names are: the file and line that declares each identifier, the
// test files that mention it, and for Go the command that tests it. A
// session that starts by searching for a name the prompt already gave spends
// a turn on a lookup that needs no model.
//
// It reads the prompt for identifiers, asks `git grep` where each is
// declared, and says nothing unless the answer is unambiguous. A hint that
// points at the wrong file costs more than no hint.
package lookup

import (
	"context"
	"os/exec"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (
	// The maxNames bound is how many identifiers of one prompt are looked up, and
	// maxSites how many declarations of one identifier still count as an
	// answer. More than that and the name is too common to point at.
	maxNames = 4
	maxSites = 2
	// The maxTests bound is how many test files are named for one identifier.
	maxTests = 2
	// The maxBeside bound is how many test files of a directory are named
	// when none mentions the identifier.
	maxBeside = 3
	// The minLength bound keeps short words, which are rarely a name worth a lookup,
	// out of the search.
	minLength = 5
)

// word is the pattern of one name.
const word = `[A-Za-z_][A-Za-z0-9_]*`

var (
	// The identifier pattern matches a word that reads as code: it has an inner capital
	// (parseID, ParseActionID), an underscore (timedelta_to_expr), or a
	// run of capitals before a word (TTLCache). A plain capitalized word
	// is prose.
	identifier = regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9]*(?:[a-z0-9][A-Z]|_[a-z0-9]|[A-Z]{2}[a-z])[A-Za-z0-9_]*\b`)
	// The declares pattern matches a line that introduces a name, with the name
	// captured, in Go, Python, TypeScript, and JavaScript.
	declares = regexp.MustCompile(`^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?` +
		`(func(?:\s*\([^)]*\))?|type|def|class|function|interface|const|let|var)\s+\*?([A-Za-z_][A-Za-z0-9_]*)`)
	// The binding pattern matches the keywords that declare a value. A capitalized word
	// bound that way is as often a label in prose ("TypeScript") as a name
	// somebody would ask about, so it only counts in lower case.
	binding = regexp.MustCompile(`^(?:const|let|var)$`)
	// The renames patterns match the two ways a prompt asks for a rename:
	// naming the old name where it says rename, or saying what the thing is
	// called and then to rename "it".
	renames = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\brename (?:the (?:[a-z]+ ){1,3})?(` + word + `)(?: in \S+)? to (` + word + `)`),
		regexp.MustCompile(`(?i)\bcalled (` + word + `)\b[^.]*\.\s+rename it to (` + word + `)`),
	}
	// The testFile pattern matches the names test files go by in those languages.
	testFile = regexp.MustCompile(`(?:_test\.go|(?:^|/)test_[^/]*\.py|_test\.py|\.(?:test|spec)\.[jt]sx?)$`)
)

// Site is where one identifier is declared and tested.
type Site struct {
	Name string
	// File is root-relative, and Line the line of the declaration.
	File string
	// Tests are the test files that mention the name, nearest first.
	Tests []string
	// RenameTo is the name the prompt asks for in place of Name, and ""
	// when it asks for no rename.
	RenameTo string
	// Beside are the test files in the declaration's directory, and are
	// only filled in when no test mentions the name.
	Beside []string
	Line   int
}

// Names returns the identifiers a prompt mentions, in the order it mentions
// them and each once.
func Names(prompt string) []string {
	var names []string

	for _, name := range identifier.FindAllString(prompt, -1) {
		if len(name) >= minLength && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}

	return names[:min(len(names), maxNames)]
}

// Find locates the identifiers a prompt names in the repository at root. An
// identifier that is declared nowhere, or in more places than can be told
// apart, is left out, and so is everything when root is not a git
// repository or ctx ends first.
func Find(ctx context.Context, root, prompt string) []Site {
	git, err := exec.LookPath("git")
	if err != nil {
		return nil
	}

	var (
		sites []Site
		paths = namedPaths(prompt)
	)

	old, renamed := renaming(prompt)

	names := Names(prompt)
	if old != "" && !slices.Contains(names, old) {
		names = append([]string{old}, names...)
	}

	for _, name := range names {
		for _, site := range located(ctx, git, root, name, paths) {
			if name == old {
				site.RenameTo = renamed
			}

			sites = append(sites, site)
		}
	}

	for _, site := range described(ctx, git, root, prompt, sites) {
		if len(site.Tests) == 0 {
			site.Tests = tests(ctx, git, root, site)
		}

		sites = append(sites, site)
	}

	for i := range sites {
		if len(sites[i].Tests) == 0 {
			sites[i].Beside = beside(ctx, git, root, sites[i].File)
		}
	}

	return sites
}

// located returns where name is declared, with the tests of each place, and
// nothing when it is declared in more places than can be told apart. A file
// or a directory among paths settles which declaration is meant.
func located(ctx context.Context, git, root, name string, paths []string) []Site {
	found := declared(ctx, git, root, name)

	if kept := slices.DeleteFunc(slices.Clone(found), func(s Site) bool {
		return !slices.Contains(paths, s.File) && !slices.Contains(paths, path.Dir(s.File))
	}); len(kept) > 0 {
		found = kept
	}

	if len(found) > maxSites {
		return nil
	}

	for i := range found {
		found[i].Tests = tests(ctx, git, root, found[i])
	}

	return found
}

// beside returns the test files tracked in file's own directory.
func beside(ctx context.Context, git, root, file string) []string {
	cmd := exec.CommandContext(ctx, git, "ls-files", "--", path.Dir(file)+"/") //nolint:gosec // git
	cmd.Dir = root

	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	var files []string

	for _, name := range strings.Fields(string(out)) {
		if testFile.MatchString(name) && path.Dir(name) == path.Dir(file) {
			files = append(files, name)
		}
	}

	return files[:min(len(files), maxBeside)]
}

// renaming reads a prompt that asks for one name in place of another, and
// returns both: "Rename the helper create_chunks in x.py to chunked", or
// "is called num, which says little. Rename it to formatNumber".
func renaming(prompt string) (string, string) {
	for _, pattern := range renames {
		if m := pattern.FindStringSubmatch(prompt); len(m) == 3 && m[1] != m[2] && !strings.EqualFold(m[1], "it") {
			return m[1], m[2]
		}
	}

	return "", ""
}

// declared returns the lines outside test files that declare name.
func declared(ctx context.Context, git, root, name string) []Site {
	var sites []Site

	for _, hit := range grep(ctx, git, root, "-n", "-w", "-F", "-e", name) {
		file, rest, ok := strings.Cut(hit, ":")
		if !ok || testFile.MatchString(file) {
			continue
		}

		number, text, ok := strings.Cut(rest, ":")
		if !ok {
			continue
		}

		line, err := strconv.Atoi(number)
		m := declares.FindStringSubmatch(text)
		if err != nil || m == nil || m[2] != name {
			continue
		}

		if first := name[0]; binding.MatchString(m[1]) && first >= 'A' && first <= 'Z' {
			continue
		}

		sites = append(sites, Site{Name: name, File: file, Line: line})
	}

	return sites
}

// tests returns the test files that mention the site's name, those in its
// own directory first.
func tests(ctx context.Context, git, root string, site Site) []string {
	var near, far []string

	for _, file := range grep(ctx, git, root, "-l", "-w", "-F", "-e", site.Name) {
		switch {
		case !testFile.MatchString(file):
		case path.Dir(file) == path.Dir(site.File):
			near = append(near, file)
		default:
			far = append(far, file)
		}
	}

	// A test beside the declaration is the one that covers it. One elsewhere
	// is named only when there is none.
	if len(near) == 0 {
		near = far
	}

	return near[:min(len(near), maxTests)]
}

// grep runs `git grep` over the tracked files under root and returns its
// lines. No match, no repository, and a ctx that ended all give none.
func grep(ctx context.Context, git, root string, args ...string) []string {
	cmd := exec.CommandContext(ctx, git, append([]string{"grep", "-I", "--no-color"}, args...)...) //nolint:gosec // git
	cmd.Dir = root

	out, err := cmd.Output()
	if err != nil {
		return nil
	}

	return strings.Split(strings.TrimRight(string(out), "\n"), "\n")
}

// Hint is the sites as the lines handed to the model, and "" for none. It
// says where to look and how to check, and nothing about what to do.
func Hint(sites []Site) string {
	if len(sites) == 0 {
		return ""
	}

	lines := make([]string, 0, len(sites)+1)

	for _, site := range sites {
		line := site.Name + " is declared at " + site.File + ":" + strconv.Itoa(site.Line)
		switch {
		case len(site.Tests) > 0:
			line += "; tests that name it: " + strings.Join(site.Tests, ", ")
		case len(site.Beside) > 0:
			line += "; no test names it, the tests beside it: " + strings.Join(site.Beside, ", ")
		}

		lines = append(lines, line)
	}

	if cmd := goRename(sites); cmd != "" {
		lines = append(lines, "To rename it everywhere it is used, tests included: "+cmd,
			"It lists the comments and strings that still say the old name, which are yours to edit.")
	} else if cmd := goTest(sites); cmd != "" {
		lines = append(lines, "To check: "+cmd)
	}

	return "wavez looked these up, so you need not search for them:\n" + strings.Join(lines, "\n")
}

// goRename is the command that makes the rename a prompt asks for, and ""
// when it asks for none or the name is not Go's.
func goRename(sites []Site) string {
	for _, site := range sites {
		// Two declarations of the name are two renames, and which is meant
		// is not this command's to guess.
		if slices.ContainsFunc(sites, func(s Site) bool { return s.Name == site.Name && s.File != site.File }) {
			continue
		}

		if site.RenameTo != "" && strings.HasSuffix(site.File, ".go") {
			return "wavez rename ./" + path.Dir(site.File) + ":" + site.Name + " " + site.RenameTo
		}
	}

	return ""
}

// goTest is the command that tests the Go packages the sites are in, and ""
// when none of them is Go.
func goTest(sites []Site) string {
	var pkgs []string

	for _, site := range sites {
		if pkg := "./" + path.Dir(site.File) + "/"; strings.HasSuffix(site.File, ".go") && !slices.Contains(pkgs, pkg) {
			pkgs = append(pkgs, pkg)
		}
	}

	if len(pkgs) == 0 {
		return ""
	}

	return "go test " + strings.Join(pkgs, " ")
}
