package route

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// pathsHeader starts the line of a state that lists the existing paths.
const pathsHeader = "existing-paths:"

// maxProbes bounds how many tokens of one prompt are looked up on disk, so
// a pasted log costs a fixed number of stats.
const maxProbes = 32

// maxListed bounds how many existing paths a state carries.
const maxListed = 8

// extension matches a token that ends like a file name.
var extension = regexp.MustCompile(`\.[A-Za-z][A-Za-z0-9]{0,5}$`)

// State builds the state the route question is asked about: prompt, and
// the paths it names that exist under root. See the package comment for the
// layout.
func State(root, prompt string) string {
	prompt = strings.TrimSpace(prompt)

	var found []string

	for i, token := range candidates(prompt) {
		if i == maxProbes || len(found) == maxListed {
			break
		}

		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(token)))
		if err != nil {
			continue
		}

		if info.IsDir() {
			token += "/"
		}

		if !slices.Contains(found, token) {
			found = append(found, token)
		}
	}

	if len(found) == 0 {
		return prompt
	}

	return prompt + "\n\n" + pathsHeader + " " + strings.Join(found, " ")
}

// candidates lists the tokens of a prompt that are shaped like a relative
// path: they hold a slash or end in an extension. A file.go:Symbol or
// file.go:12 token gives its file.
func candidates(prompt string) []string {
	var out []string

	for field := range strings.FieldsSeq(prompt) {
		token := strings.Trim(field, "\"'`()[]{}<>,;!?*")
		token = strings.TrimSuffix(strings.TrimSuffix(token, "'s"), "’s")

		if strings.Contains(token, "://") {
			continue
		}

		if at := strings.IndexByte(token, ':'); at >= 0 {
			token = token[:at]
		}

		token = strings.TrimSuffix(strings.TrimRight(token, "."), "/")
		token = strings.TrimPrefix(token, "./")

		if !strings.Contains(token, "/") && !extension.MatchString(token) {
			continue
		}

		if !filepath.IsLocal(filepath.FromSlash(token)) || slices.Contains(out, token) {
			continue
		}

		out = append(out, token)
	}

	return out
}

// parsed is a state taken apart again.
type parsed struct {
	prompt string
	// files and dirs are the existing paths the state lists.
	files []string
	dirs  []string
}

func parse(state string) parsed {
	out := parsed{prompt: strings.TrimSpace(state)}

	at := strings.LastIndex(state, "\n"+pathsHeader)
	if at < 0 || strings.Contains(state[at+1:], "\n") {
		return out
	}

	out.prompt = strings.TrimSpace(state[:at])

	for path := range strings.FieldsSeq(state[at+1+len(pathsHeader):]) {
		if strings.HasSuffix(path, "/") {
			out.dirs = append(out.dirs, path)

			continue
		}

		out.files = append(out.files, path)
	}

	return out
}
