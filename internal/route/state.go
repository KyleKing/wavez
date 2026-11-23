package route

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/kyleking/wavez/internal/decide"
)

// pathsHeader starts the line of a state that lists the existing paths.
const pathsHeader = "existing-paths:"

// cutMarker is the line that ends a prompt too long to carry whole.
const cutMarker = "[prompt cut]"

// maxProbes bounds how many tokens of one prompt are looked up on disk, so
// a pasted log costs a fixed number of stats.
const maxProbes = 32

// maxListed bounds how many existing paths a state carries.
const maxListed = 8

// The bytes of a state. The paths line is the part code verified, so it is
// given its room first and the prompt is cut to what remains: a state never
// passes decide.StateLimit, and a model is never sent one with its paths
// cut off.
const (
	pathsBudget = 480
	promptLimit = decide.StateLimit - pathsBudget - len(cutMarker) - len("\n\n\n")
)

// extension matches a token that ends like a file name.
var extension = regexp.MustCompile(`\.[A-Za-z][A-Za-z0-9]{0,5}$`)

// State builds the state the route question is asked about: prompt, and
// the paths it names that exist under root. See the package comment for the
// layout. A prompt longer than the state has room for is cut and ends with
// a line saying so; the paths are still looked for in all of it.
func State(root, prompt string) string {
	prompt = strings.TrimSpace(prompt)

	var found []string

	line := pathsHeader

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

		if slices.Contains(found, token) || len(line)+len(" ")+len(token) > pathsBudget {
			continue
		}

		found = append(found, token)
		line += " " + token
	}

	if len(prompt) > promptLimit {
		prompt = cut(prompt, promptLimit) + "\n" + cutMarker
	}

	if len(found) == 0 {
		return prompt
	}

	return prompt + "\n\n" + line
}

// cut shortens s to at most limit bytes without splitting a character.
func cut(s string, limit int) string {
	for limit > 0 && !utf8.RuneStart(s[limit]) {
		limit--
	}

	return s[:limit]
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
	// paths is the existing paths the state lists, a directory without
	// its slash.
	paths []string
	// cut says the prompt is not all there, so nothing about its whole
	// shape can be checked.
	cut bool
}

func parse(state string) parsed {
	out := parsed{prompt: strings.TrimSpace(state)}

	if at := strings.LastIndex(state, "\n"+pathsHeader); at >= 0 && !strings.Contains(state[at+1:], "\n") {
		out.prompt = strings.TrimSpace(state[:at])

		for path := range strings.FieldsSeq(state[at+1+len(pathsHeader):]) {
			out.paths = append(out.paths, strings.TrimSuffix(path, "/"))
		}
	}

	if rest, ok := strings.CutSuffix(out.prompt, "\n"+cutMarker); ok {
		out.prompt, out.cut = rest, true
	}

	return out
}
