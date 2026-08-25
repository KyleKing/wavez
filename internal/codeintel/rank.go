package codeintel

import (
	"sort"
	"strings"
)

// fuzzyScan bounds how many rows one fuzzy query ranks before it answers.
// The index scores by document length, so a short query puts the shortest
// documents first whether or not they hold it as a word: measured on this
// repository, `Read` put twelve names built from `Thread` above the two
// symbols actually called Read, and the second of those sat at row 90 of
// 1,239.
const fuzzyScan = 200

// fuzzyRow is one matched FTS row before it is hydrated, which is where
// ranking happens: a symbol row carries its name on the first line of the
// indexed text, so the order is decided without reading the store again.
type fuzzyRow struct {
	kind  string
	text  string
	refID int64
}

// rankFuzzy puts the rows a name match speaks for first, exact names ahead
// of names the query is a word of, shorter names ahead of longer ones
// because the query covers more of them, and leaves everything else in the
// bm25 order it arrived in.
func rankFuzzy(rows []fuzzyRow, terms []string) {
	sort.SliceStable(rows, func(i, j int) bool {
		ri, rj := nameRank(rows[i], terms), nameRank(rows[j], terms)
		if ri != rj {
			return ri < rj
		}

		if ri == unnamed {
			return false
		}

		return len(symbolName(rows[i])) < len(symbolName(rows[j]))
	})
}

const (
	namedExactly = iota
	namedAsWord
	unnamed
)

func nameRank(row fuzzyRow, terms []string) int {
	name := symbolName(row)
	if name == "" {
		return unnamed
	}

	rank := unnamed

	for _, term := range terms {
		switch {
		case strings.EqualFold(name, term):
			return namedExactly
		case WordAligned(name, term):
			rank = namedAsWord
		}
	}

	return rank
}

// symbolName is the name a symbol row was indexed under, empty for a path
// or file row. The indexed text opens with the name, so this reads it
// rather than resolving the symbol.
func symbolName(row fuzzyRow) string {
	if row.kind != "symbol" {
		return ""
	}

	name, _, _ := strings.Cut(row.text, "\n")

	return name
}

// WordAligned reports whether query occurs in name as a whole word, where a
// word opens at the name's start, at an underscore, or at an upper-case
// letter following a lower-case one, and closes the same way.
//
// It is what separates a near miss from a collision between names that
// merely share letters. `Read` is a word of `NewRead` and three letters in
// the middle of `OpenThread`, and only the first answers a search for it.
func WordAligned(name, query string) bool {
	if name == query {
		return true
	}

	if len(query) > len(name) {
		name, query = query, name
	}

	if query == "" {
		return false
	}

	for at := 0; at+len(query) <= len(name); {
		i := strings.Index(name[at:], query)
		if i < 0 {
			return false
		}

		i += at
		if wordOpens(name, i) && wordOpens(name, i+len(query)) {
			return true
		}

		at = i + 1
	}

	return false
}

// wordOpens reports whether a CamelCase or underscore word starts at i, with
// the end of the name counting as one so a query that is the name's tail
// aligns.
func wordOpens(name string, i int) bool {
	if i == 0 || i == len(name) {
		return true
	}

	if name[i] == '_' || name[i-1] == '_' {
		return true
	}

	return isUpperByte(name[i]) && !isUpperByte(name[i-1])
}

func isUpperByte(b byte) bool { return b >= 'A' && b <= 'Z' }
