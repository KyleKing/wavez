package guard

import (
	"path/filepath"
	"strings"
)

// Env is the machine context a classification resolves paths against. It is
// an argument rather than something this package reads for itself, so a
// verdict stays a pure function of its inputs: the same command with the
// same Env classifies the same way on any machine, which is what makes a
// guard decision reproducible and testable.
type Env struct {
	// ProjectRoot is the directory a path is judged against. A target at or
	// outside it is what the destructive rules refuse.
	ProjectRoot string
	// Home and TempDir expand the two variables a destructive command most
	// often hides a path behind. Empty means unknown, which leaves the
	// variable unexpanded and therefore unresolved.
	Home    string
	TempDir string
}

// unresolvedChars mark a path this package cannot reduce to one location:
// a variable it does not know, a command substitution, or a glob that
// stands for an unknown set of files.
const unresolvedChars = "$`*?[]{}"

// expandPath substitutes the variables Env knows and reports whether the
// result names one definite location. It deliberately expands a short list
// rather than every variable a shell would: a name this package cannot
// resolve must stay unresolved so the caller fails closed on it, and
// guessing would be worse than admitting the gap.
func (e Env) expandPath(target string) (string, bool) {
	out := target

	if e.Home != "" {
		if out == "~" {
			out = e.Home
		} else if rest, ok := strings.CutPrefix(out, "~/"); ok {
			out = filepath.Join(e.Home, rest)
		}

		out = replaceVar(out, "HOME", e.Home)
	}

	if e.TempDir != "" {
		out = replaceVar(out, "TMPDIR", e.TempDir)
	}

	if e.ProjectRoot != "" {
		out = replaceVar(out, "PWD", e.ProjectRoot)
	}

	return out, !strings.ContainsAny(out, unresolvedChars)
}

// replaceVar substitutes both `$NAME` and `${NAME}`. The braced form is
// replaced first so the bare form's match cannot leave a stray brace.
func replaceVar(s, name, value string) string {
	s = strings.ReplaceAll(s, "${"+name+"}", value)

	for {
		i := strings.Index(s, "$"+name)
		if i < 0 {
			return s
		}

		after := i + len("$"+name)
		// `$HOMEWORK` is a different variable, so only replace when the name
		// ends at a path separator or at the end of the token.
		if after < len(s) && (isWordChar(rune(s[after]))) {
			return s
		}

		s = s[:i] + value + s[after:]
	}
}

func isWordChar(r rune) bool {
	return r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}
