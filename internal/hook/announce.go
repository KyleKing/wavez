package hook

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/kyleking/wavez/internal/config"
)

// AnnounceEnv names the environment variable that, set to "0", keeps a
// SessionStart from telling the agent the gates run by themselves. A
// benchmark sets it for one arm to measure what the sentence is worth.
const AnnounceEnv = "WAVEZ_ANNOUNCE"

// maxNamedChecks is how many of a project's own checks the announcement
// names before it counts the rest.
const maxNamedChecks = 3

// announced answers a SessionStart with the one sentence that tells the
// agent the gates exist. An agent that does not know runs `go build ./...`
// and `go test ./...` by hand before it stops, and then the gates run them
// again: the first design measured eight such sweeps a session without the
// sentence and none with it.
//
// It says nothing in a project where no check would run, nothing when
// turned off, and nothing on a resume, whose context already holds it.
func (h *Handler) announced(ev Event, rec *record) output {
	if h.quiet || ev.Source == sourceResume {
		return output{}
	}

	builtins, custom, err := h.checks(rec.Root)
	if err != nil {
		return output{}
	}

	text := announcement(builtins, custom)
	if text == "" {
		return output{}
	}

	rec.Note = "announced the gates"

	return contextFor(eventSessionStart, text)
}

// announcement is the sentence for a project with these checks enabled,
// and "" for a project with none. It is fixed text over a closed set of
// words plus the project's own check names, and stays short because the
// agent pays for it on every turn of the session.
func announcement(builtins, custom []string) string {
	words := map[string]string{
		config.BuiltinFormat: "format", config.BuiltinBuild: "build",
		config.BuiltinTest: "tests", config.BuiltinLint: "lint",
	}

	var names, sweeps []string

	for _, name := range builtins {
		names = append(names, words[name])

		if name != config.BuiltinFormat {
			sweeps = append(sweeps, strings.TrimSuffix(words[name], "s"))
		}
	}

	names = append(names, custom[:min(len(custom), maxNamedChecks)]...)
	if more := len(custom) - maxNamedChecks; more > 0 {
		names = append(names, plural(more, "other check"))
	}

	if len(names) == 0 {
		return ""
	}

	scope, verb := "packages", "run"
	if len(builtins) == 0 {
		scope = "files"
	}

	if len(names) == 1 && !strings.HasSuffix(names[0], "s") {
		verb = "runs"
	}

	avoid := "do not run them yourself."
	if len(sweeps) > 0 {
		avoid = "do not run whole-project " + list(sweeps, "or") + " commands yourself."
	}

	return capitalize(list(names, "and")) + " for the " + scope + " you change " + verb +
		" automatically when you finish; " + avoid
}

// list joins words as prose: "a", "a and b", "a, b and c".
func list(words []string, last string) string {
	if len(words) < 2 { //nolint:mnd // one word or none needs no joining
		return strings.Join(words, "")
	}

	head := slices.Clone(words[:len(words)-1])

	return strings.Join(head, ", ") + " " + last + " " + words[len(words)-1]
}

func capitalize(text string) string {
	runes := []rune(text)
	if len(runes) > 0 {
		runes[0] = unicode.ToUpper(runes[0])
	}

	return string(runes)
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}

	return fmt.Sprintf("%d %ss", n, noun)
}
