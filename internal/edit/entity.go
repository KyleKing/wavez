package edit

import (
	"html"
	"strings"
	"unicode"
	"unicode/utf8"
)

// legacyEntities are the HTML character references that need no semicolon,
// so an ampersand followed by one of these names renders as a single rune
// wherever something templates text as HTML. In Go source an ampersand
// followed by a name is an address-of expression, which is how a model
// asking for `&notUnique` had it arrive as `¬Unique`: it could not write
// the identifier at all, and one replay lane sent the same anchor five
// times before dying.
//
// Only the names are listed. The runes come from the standard library, so a
// name that is not a real reference expands to itself and is skipped rather
// than mapping something wrong.
var legacyEntities = []string{
	"acute", "amp", "cedil", "cent", "copy", "curren", "deg", "divide", "eth",
	"frac12", "frac14", "frac34", "gt", "iexcl", "iquest", "laquo", "lt", "macr",
	"micro", "middot", "nbsp", "not", "ordf", "ordm", "para", "plusmn", "pound",
	"quot", "raquo", "reg", "sect", "shy", "sup1", "sup2", "sup3", "szlig",
	"thorn", "times", "uml", "yen",
}

// entityRunes maps each collapsed rune back to the ampersand and name that
// produced it. A name whose rune is ASCII is left out: `&quot`, `&amp`,
// `&lt`, and `&gt` collapse to characters source code is full of, so
// restoring them rewrites every string literal that opens with a word into
// `&quotWord`. That corruption made the repaired anchor fail and the
// original error stand, which is how the repair looked like it had never
// run.
var entityRunes = buildEntityRunes()

func buildEntityRunes() map[rune]string {
	out := make(map[rune]string, len(legacyEntities))

	for _, name := range legacyEntities {
		expanded := html.UnescapeString("&" + name)
		if expanded == "&"+name {
			continue
		}

		runes := []rune(expanded)
		if len(runes) != 1 || runes[0] < utf8.RuneSelf {
			continue
		}

		if _, taken := out[runes[0]]; !taken {
			out[runes[0]] = "&" + name
		}
	}

	return out
}

// restoreEntities puts back the ampersand and name behind every collapsed
// rune that is followed by an identifier character, which is the shape a
// mangled address-of expression has. A rune standing on its own is left
// alone: there it is far likelier to be the symbol the writer meant.
//
// It returns the text unchanged and false when there was nothing to put
// back, so a caller can tell a repair from a no-op.
func restoreEntities(s string) (string, bool) {
	runes := []rune(s)

	var (
		b       strings.Builder
		changed bool
	)

	for i, r := range runes {
		name, ok := entityRunes[r]
		if !ok || i+1 >= len(runes) || !identRune(runes[i+1]) {
			b.WriteRune(r)

			continue
		}

		b.WriteString(name)

		changed = true
	}

	return b.String(), changed
}

func identRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
