package lookup

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (
	// The wordLength bound keeps short words, which match half the names in
	// any package, out of the search by word.
	wordLength = 5
	// The phraseLength bound is the shortest quoted phrase worth a search.
	phraseLength = 12
)

var (
	// The pathToken pattern matches a word with a slash in it, which a
	// prompt uses for a directory or a file.
	pathToken = regexp.MustCompile(`[A-Za-z0-9_.\-]+(?:/[A-Za-z0-9_.\-]+)+`)
	// The quoted pattern matches text between a pair of quotes or backticks.
	quoted = regexp.MustCompile("'([^'\n]+)'|\"([^\"\n]+)\"|`([^`\n]+)`")
	// The declaring pattern is what `git grep -E` is asked for: a line that
	// opens a function or a class. It is written in POSIX classes, because
	// git's own expressions have no \s or \b where it is built without PCRE,
	// as it is on macOS.
	declaring = `^[[:space:]]*(export[[:space:]]+)?(async[[:space:]]+)?(func|def|class|function)[[:space:]]`
	// The wordToken pattern matches a word, with the hyphens a compound is
	// written with.
	wordToken = regexp.MustCompile(`[A-Za-z][A-Za-z\-]+`)
)

// described finds what a prompt points at without naming it: a declaration
// in a directory the prompt names whose name holds one of the prompt's
// words ("the password rule in pkg/validator"), and the declaration around
// a string the prompt quotes ("refuses to start with 'unsupported TLS
// minimum version'"). Names already found are left out.
func described(ctx context.Context, git, root, prompt string, have []Site) []Site {
	var sites []Site

	add := func(site Site) {
		known := func(s Site) bool { return s.File == site.File && s.Name == site.Name }
		if site.Name != "" && !slices.ContainsFunc(have, known) && !slices.ContainsFunc(sites, known) {
			sites = append(sites, site)
		}
	}

	for _, dir := range namedDirs(root, prompt) {
		for _, site := range byWord(ctx, git, root, dir, prompt) {
			add(site)
		}
	}

	for _, phrase := range phrases(prompt) {
		hits := slices.DeleteFunc(grep(ctx, git, root, "-n", "-F", "-e", phrase), func(hit string) bool {
			file, _, _ := strings.Cut(hit, ":")

			return hit == "" || testFile.MatchString(file)
		})
		if len(hits) == 0 || len(hits) > maxSites {
			continue
		}

		for _, hit := range hits {
			add(enclosing(root, hit))
		}
	}

	return sites[:min(len(sites), maxNames)]
}

// namedDirs returns the directories the prompt names that exist under root:
// a path that is one, or the directory of a path that is a file.
func namedDirs(root, prompt string) []string {
	var dirs []string

	for _, token := range pathToken.FindAllString(prompt, -1) {
		token = strings.Trim(token, "./")
		if !filepath.IsLocal(filepath.FromSlash(token)) {
			continue
		}

		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(token)))
		if err != nil {
			continue
		}

		dir := token
		if !info.IsDir() {
			dir = filepath.ToSlash(filepath.Dir(filepath.FromSlash(token)))
		}

		if !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}

	return dirs
}

// phrases returns the quoted parts of a prompt long enough to be a message
// and not a value.
func phrases(prompt string) []string {
	var out []string

	for _, m := range quoted.FindAllStringSubmatch(prompt, -1) {
		phrase := m[1] + m[2] + m[3]
		if len(phrase) >= phraseLength && strings.Contains(phrase, " ") && !slices.Contains(out, phrase) {
			out = append(out, phrase)
		}
	}

	return out
}

// words returns the prompt's words as a declaration's name could hold
// them: lower case, a compound's hyphens closed up, and nothing that is a
// part of the path itself, which every name in the directory may hold.
func words(prompt, dir string) []string {
	var out []string

	for _, token := range wordToken.FindAllString(pathToken.ReplaceAllString(prompt, " "), -1) {
		word := strings.ToLower(strings.ReplaceAll(token, "-", ""))
		if len(word) >= wordLength && !strings.Contains(strings.ToLower(dir), word) && !slices.Contains(out, word) {
			out = append(out, word)
		}
	}

	return out
}

// byWord returns the declarations directly in dir whose names hold the most
// letters of the prompt's words, when that is at least one word and at most
// maxSites declarations share the lead.
func byWord(ctx context.Context, git, root, dir, prompt string) []Site {
	var (
		best   = make([]Site, 0, maxSites)
		score  int
		wanted = words(prompt, dir)
	)

	for _, hit := range grep(ctx, git, root, "-n", "-E", "-e", declaring,
		"--", dir+"/*") {
		site, ok := declaredOn(hit)
		if !ok || testFile.MatchString(site.File) || filepath.ToSlash(filepath.Dir(site.File)) != dir {
			continue
		}

		name, held := strings.ToLower(site.Name), 0

		// A longer word says more about which name is meant.
		for _, word := range wanted {
			if strings.Contains(name, word) {
				held += len(word)
			}
		}

		switch {
		case held == 0 || held < score:
		case held > score:
			best, score = append(best[:0], site), held
		default:
			best = append(best, site)
		}
	}

	if len(best) > maxSites {
		return nil
	}

	for i := range best {
		best[i].Tests = tests(ctx, git, root, best[i])
	}

	return best
}

// declaredOn reads a `git grep -n` line that holds a declaration.
func declaredOn(hit string) (Site, bool) {
	file, rest, ok := strings.Cut(hit, ":")
	if !ok {
		return Site{}, false
	}

	number, text, ok := strings.Cut(rest, ":")
	if !ok {
		return Site{}, false
	}

	line, err := strconv.Atoi(number)
	m := declares.FindStringSubmatch(text)

	if err != nil || m == nil {
		return Site{}, false
	}

	return Site{Name: m[2], File: file, Line: line}, true
}

// enclosing is the declaration around a `git grep -n` hit: the nearest line
// at or above it that declares something and is indented no deeper.
func enclosing(root, hit string) Site {
	file, rest, ok := strings.Cut(hit, ":")
	if !ok {
		return Site{}
	}

	number, _, _ := strings.Cut(rest, ":")

	at, err := strconv.Atoi(number)
	lines, read := read(root, file)

	if err != nil || !read || at < 1 || at > len(lines) {
		return Site{}
	}

	depth := indent(lines[at-1])

	for i := at; i >= 1; i-- {
		if m := declares.FindStringSubmatch(lines[i-1]); m != nil && indent(lines[i-1]) <= depth &&
			!binding.MatchString(m[1]) {
			return Site{Name: m[2], File: file, Line: i}
		}
	}

	return Site{}
}
