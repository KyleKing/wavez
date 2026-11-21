package refactor

import (
	"sort"
	"strings"
)

// A name is offered as a near miss when it is within a third of the wanted
// name's length in edits, and never fewer than two.
const (
	maxNear     = 5
	nearDivisor = 3
	minNearEdit = 2
)

// nearNames picks the names a caller most likely meant by want: the same
// letters in another case, a name containing it or contained in it, or one
// within a few edits.
func nearNames(want string, names []string) []string {
	type scored struct {
		name  string
		score int
	}

	limit := max(minNearEdit, len(want)/nearDivisor)
	lower := strings.ToLower(want)
	seen := map[string]bool{}

	var hits []scored

	for _, name := range names {
		if seen[name] {
			continue
		}

		seen[name] = true

		if score, ok := nearness(lower, strings.ToLower(name), limit); ok {
			hits = append(hits, scored{name: name, score: score})
		}
	}

	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score < hits[j].score })

	out := make([]string, 0, maxNear)
	for i := 0; i < len(hits) && i < maxNear; i++ {
		out = append(out, hits[i].name)
	}

	return out
}

func nearness(want, have string, limit int) (int, bool) {
	if want == have {
		return 0, true
	}

	if dist := editDistance(want, have); dist <= limit {
		return dist, true
	}

	if strings.Contains(have, want) || strings.Contains(want, have) {
		return limit + 1, true
	}

	return 0, false
}

// editDistance is the Levenshtein distance between two strings, by byte.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i

		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}

			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}

		prev = cur
	}

	return prev[len(b)]
}
