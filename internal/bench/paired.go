package bench

import (
	"fmt"
	"math"
	mathrand "math/rand/v2"
	"slices"
	"strings"
)

// MinPairedTasks is how many tasks two arms must share before the summary
// across tasks gives a verdict. Five tasks cannot reach Alpha even when
// every one of them moves the same way.
const MinPairedTasks = 6

// pairing is one task both arms ran under one setup.
type pairing struct {
	base  *group
	other *group
}

// across is everything but the task that two groups must share for their
// tasks to be summarized together.
func (k groupKey) across() groupKey {
	k.arm, k.task, k.hash = "", "", ""

	return k
}

// reportAcross writes the summary across tasks: for each arm other than
// the one compared against, and each harness, model, build, and flags they
// both ran under, one line per task with the arm's medians set against the
// other's, and then one verdict per metric from a paired test on those
// per-task numbers. A task is its own control here, so a task that costs
// ten times another weighs the same, and raw turns are never pooled across
// tasks.
func reportAcross(b *strings.Builder, groups []group, against Arm) {
	var (
		arms   []Arm
		setups []groupKey
	)

	for i := range groups {
		if arm := groups[i].key.arm; arm != against && !slices.Contains(arms, arm) {
			arms = append(arms, arm)
		}

		if key := groups[i].key.across(); !slices.Contains(setups, key) {
			setups = append(setups, key)
		}
	}

	slices.SortStableFunc(arms, func(x, y Arm) int { return armRank(x) - armRank(y) })

	for _, arm := range arms {
		for _, setup := range setups {
			if pairs := pairings(groups, setup, against, arm); len(pairs) > 0 {
				writeAcross(b, setup, pairs)
			}
		}
	}
}

// pairings finds, for each task, the two arms' groups under one setup. A
// task whose definition changed has more than one; the one with the most
// measured runs in its thinner arm is used, and a task with no measured
// run in either arm is left out.
func pairings(groups []group, setup groupKey, against, arm Arm) []pairing {
	var (
		pairs []pairing
		tasks []string
	)

	for i := range groups {
		base := &groups[i]
		if base.key.arm != against || base.key.across() != setup {
			continue
		}

		for j := range groups {
			other := &groups[j]
			if other.key.arm != arm || other.key.setup() != base.key.setup() {
				continue
			}

			found := pairing{base: base, other: other}
			if found.thinner() == 0 {
				continue
			}

			at := slices.Index(tasks, base.key.task)
			if at < 0 {
				tasks, pairs = append(tasks, base.key.task), append(pairs, found)
			} else if found.thinner() > pairs[at].thinner() {
				pairs[at] = found
			}
		}
	}

	return pairs
}

// thinner is the measured runs of whichever arm has fewer.
func (p pairing) thinner() int {
	return min(p.base.measured(), p.other.measured())
}

const acrossRow = "  %-12s %8s %9s %8s %7s\n"

func writeAcross(b *strings.Builder, setup groupKey, pairs []pairing) {
	base, other := pairs[0].base.key.arm, pairs[0].other.key.arm

	fmt.Fprintf(b, "\nacross tasks, %s/%s: %s, served %s, build %s, flags %s\n", other, base,
		setup.harness, orDash(setup.served), orUnknown(setup.build), orUnknown(setup.flags))
	fmt.Fprintf(b, acrossRow, "task", "turns", "cost", "wall", "sweeps")

	ratioMetrics := []metric{metricTurns, metricCost, metricWall}
	logs := make([][]float64, len(ratioMetrics))

	var (
		sweeps []float64
		passed [2]tallyOf
	)

	for _, p := range pairs {
		cells := make([]any, 0, len(ratioMetrics)+2)
		cells = append(cells, p.base.key.task)

		for i := range ratioMetrics {
			ratio, ok := ratioOf(&ratioMetrics[i], p)
			if ok {
				logs[i] = append(logs[i], math.Log(ratio))
			}

			cells = append(cells, cellOr(ok, "%.2fx", ratio))
		}

		diff, ok := sweepDiff(p)
		if ok {
			sweeps = append(sweeps, diff)
		}

		fmt.Fprintf(b, acrossRow, append(cells, cellOr(ok, "%+g", diff))...)

		passed[0].add(p.other)
		passed[1].add(p.base)
	}

	fmt.Fprintf(b, "  pass: %s %d/%d runs, %s %d/%d runs\n",
		other, passed[0].pass, passed[0].runs, base, passed[1].pass, passed[1].runs)

	// The arm that used less has to have passed as often for that to be a
	// win: doing less of the work is the cheapest way to spend less.
	caveat := func(otherUsedLess bool) string {
		cheap, dear, name := passed[0], passed[1], other
		if !otherUsedLess {
			cheap, dear, name = passed[1], passed[0], base
		}

		if cheap.pass*dear.runs < dear.pass*cheap.runs {
			return fmt.Sprintf(", but not a win: %s passed fewer of its runs", name)
		}

		return ""
	}

	for i := range ratioMetrics {
		mean := meanOf(logs[i])
		fmt.Fprintf(b, "  %-7s %s\n", ratioMetrics[i].name,
			pairedVerdict(cellOr(len(logs[i]) > 0, "%5.2fx", math.Exp(mean)), logs[i], caveat(mean < 0)))
	}

	mean := meanOf(sweeps)
	fmt.Fprintf(b, "  %-7s %s\n", metricSweeps.name,
		pairedVerdict(cellOr(len(sweeps) > 0, "%+6.2f", mean), sweeps, caveat(mean < 0)))
}

// tallyOf counts passing runs.
type tallyOf struct {
	pass int
	runs int
}

func (t *tallyOf) add(g *group) {
	t.pass += g.count(func(r *Record) bool { return r.Pass })
	t.runs += len(g.recs)
}

// ratioOf is the other arm's median over the base arm's for one task, when
// both measured the metric and the base's is not zero.
func ratioOf(m *metric, p pairing) (float64, bool) {
	base, other := m.values(p.base), m.values(p.other)
	if len(base) == 0 || len(other) == 0 || median(base) <= 0 || median(other) <= 0 {
		return 0, false
	}

	return median(other) / median(base), true
}

// sweepDiff is the other arm's median sweeps less the base arm's. Sweeps
// are compared by difference because the number wanted is zero, and zero
// has no ratio.
func sweepDiff(p pairing) (float64, bool) {
	base, other := metricSweeps.values(p.base), metricSweeps.values(p.other)
	if len(base) == 0 || len(other) == 0 {
		return 0, false
	}

	return median(other) - median(base), true
}

func cellOr(ok bool, format string, v float64) string {
	if !ok {
		return "-"
	}

	return fmt.Sprintf(format, v)
}

func meanOf(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	sum := 0.0
	for _, v := range values {
		sum += v
	}

	return sum / float64(len(values))
}

// pairedVerdict renders one metric of the summary across tasks: the
// geometric mean ratio (or mean difference), how many tasks it is over,
// and the sign-flip test's verdict.
func pairedVerdict(center string, perTask []float64, caveat string) string {
	text := fmt.Sprintf("%s  n %d tasks  ", center, len(perTask))

	if len(perTask) < MinPairedTasks {
		return text + fmt.Sprintf("no verdict: fewer than %d paired tasks", MinPairedTasks)
	}

	p := signFlipP(perTask)
	text += fmt.Sprintf("p %.3f  ", p)

	if p <= Alpha {
		return text + "outside the noise" + caveat
	}

	return text + "inside the noise"
}

// Bounds on the sign-flip test.
const (
	// The most paired values whose sign assignments are all counted. Above
	// it they are sampled.
	flipLimit = 20
	// How many assignments are drawn then, always the same ones, so a
	// report over the same records never changes its mind.
	flipSamples = 100_000
	flipSeed    = 1
	// Keeps a sum that differs from the observed one only by rounding on
	// the observed one's side.
	flipSlack = 1e-9
)

// signFlipP is the two-sided p-value of the paired sign-flip permutation
// test. Each value is one task's difference between the arms (a log ratio
// or a plain difference). If the arms were the same, each difference was
// as likely to have come out with the other sign, so the test asks what
// share of all the ways of flipping signs gives a sum at least as far from
// zero as the one seen.
func signFlipP(values []float64) float64 {
	if len(values) == 0 {
		return 1
	}

	observed := 0.0
	for _, v := range values {
		observed += v
	}

	bar := math.Abs(observed) - flipSlack*(1+math.Abs(observed))

	flipped := func(signs uint64) bool {
		sum := 0.0

		for i, v := range values {
			if signs>>uint(i)&1 == 1 {
				v = -v
			}

			sum += v
		}

		return math.Abs(sum) >= bar
	}

	if len(values) <= flipLimit {
		far, all := 0, uint64(1)<<uint(len(values))

		for signs := range all {
			if flipped(signs) {
				far++
			}
		}

		return float64(far) / float64(all)
	}

	// The draws decide nothing secret and must repeat.
	rng := mathrand.New(mathrand.NewPCG(flipSeed, flipSeed)) //nolint:gosec // see above
	far := 1

	for range flipSamples {
		if flippedMany(values, rng, bar) {
			far++
		}
	}

	return float64(far) / float64(flipSamples+1)
}

// flippedMany draws one sign per value, for more values than a word has
// bits.
func flippedMany(values []float64, rng *mathrand.Rand, bar float64) bool {
	sum := 0.0

	for _, v := range values {
		if rng.Uint64()&1 == 1 {
			v = -v
		}

		sum += v
	}

	return math.Abs(sum) >= bar
}
