package bench

import (
	"math"
	"slices"
)

// exactLimit is the largest pooled sample whose rank-sum distribution is
// counted exactly. Above it the normal approximation is close enough.
const exactLimit = 20

// rankSumP is the two-sided p-value of the Mann-Whitney rank-sum test: how
// likely a rank sum at least as far from its expectation as the one seen
// would be if both samples came from the same distribution. It asks only
// how the samples interleave when sorted together, so it assumes nothing
// about the shape of either, and it takes both their spread and their size
// into account, which a ratio of medians does not. An empty sample has no
// test and returns 1.
func rankSumP(a, b []float64) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 1
	}

	ranks, ties := midranks(a, b)
	total := len(a) + len(b)

	// Ranks are kept doubled, which makes the mean rank of a tie a whole
	// number.
	seen := 0
	for _, r := range ranks[:len(a)] {
		seen += r
	}

	expected := len(a) * (total + 1)
	distance := abs(seen - expected)

	if total <= exactLimit {
		return exactP(ranks, len(a), expected, distance)
	}

	return normalP(len(a), len(b), ties, float64(distance)/2) //nolint:mnd // back from doubled ranks
}

// midranks ranks the pooled values, giving tied values the mean of the
// ranks they span. It returns twice each rank, a's first and then b's, and
// the size of every group of tied values.
func midranks(a, b []float64) ([]int, []int) {
	pooled := slices.Concat(a, b)

	order := make([]int, len(pooled))
	for i := range order {
		order[i] = i
	}

	slices.SortFunc(order, func(x, y int) int {
		switch {
		case pooled[x] < pooled[y]:
			return -1
		case pooled[x] > pooled[y]:
			return 1
		default:
			return 0
		}
	})

	ranks := make([]int, len(pooled))

	var ties []int

	for start := 0; start < len(order); {
		end := start
		for end < len(order) && pooled[order[end]] == pooled[order[start]] {
			end++
		}

		// Positions start+1 through end, whose mean doubled is their sum.
		for _, at := range order[start:end] {
			ranks[at] = start + 1 + end
		}

		ties = append(ties, end-start)
		start = end
	}

	return ranks, ties
}

// exactP counts, over every way of choosing which n of the pooled ranks
// belong to the first sample, the share whose rank sum is at least as far
// from expected as distance.
func exactP(ranks []int, n, expected, distance int) float64 {
	sum := 0
	for _, r := range ranks {
		sum += r
	}

	// ways[k][s] is how many choices of k ranks add up to s.
	ways := make([][]float64, n+1)
	for k := range ways {
		ways[k] = make([]float64, sum+1)
	}

	ways[0][0] = 1

	for _, r := range ranks {
		for k := n; k >= 1; k-- {
			for s := sum; s >= r; s-- {
				ways[k][s] += ways[k-1][s-r]
			}
		}
	}

	var all, far float64

	for s, count := range ways[n] {
		all += count

		if abs(s-expected) >= distance {
			far += count
		}
	}

	return far / all
}

// normalP approximates the same share with a normal distribution whose
// variance is corrected for ties, moving the distance half a rank inward
// because rank sums come in steps.
func normalP(n1, n2 int, ties []int, distance float64) float64 {
	const (
		continuity = 0.5
		pairs      = 12
	)

	total := float64(n1 + n2)

	var tied float64

	for _, t := range ties {
		size := float64(t)
		tied += size*size*size - size
	}

	variance := float64(n1) * float64(n2) / pairs * (total + 1 - tied/(total*(total-1)))
	if variance <= 0 {
		return 1
	}

	z := max(distance-continuity, 0) / math.Sqrt(variance)

	return math.Erfc(z / math.Sqrt2)
}

func abs(v int) int {
	if v < 0 {
		return -v
	}

	return v
}
