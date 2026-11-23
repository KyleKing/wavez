package bench

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"
)

// What a difference between two arms has to clear before it is a result.
// One task measured 40-70% run-to-run variance in turns on an unchanged
// tree, so a ratio alone says nothing: the question is whether the two
// arms' runs could have been drawn from the same pile.
const (
	// MinRuns is how many measured runs each arm needs. Fewer cannot reach
	// Alpha however far apart the arms are.
	MinRuns = 5
	// Alpha is the largest p-value of the rank-sum test that counts as
	// outside the noise.
	Alpha = 0.05
)

// ReportOptions narrows and aims a report.
type ReportOptions struct {
	// Task is the one task to report, every task when empty.
	Task string
	// Against is the arm every other arm is divided by, the baseline when
	// empty.
	Against Arm
}

// group is the runs of one task that may be summarized together: same arm
// and same setup.
type group struct {
	key  groupKey
	recs []Record
}

type groupKey struct {
	task    string
	arm     Arm
	harness string
	served  string
	hash    string
	build   string
	flags   string
}

// setup is everything two groups must share to be compared: the harness,
// the model that answered, the task's definition, the wavez build, and the
// flags of the invocation.
func (k groupKey) setup() groupKey {
	k.arm = ""

	return k
}

// Report writes, for each task (or the one named), a table per setup with a
// row per arm and then, for each arm other than the one compared against,
// its ratio to that arm on each metric with the rank-sum test's verdict on
// whether the difference is outside the noise. Arms that never shared a
// setup are not compared at all A report of every task ends with the
// summary across tasks, which is where a small effect can show: one task's
// handful of runs cannot separate arms a fifth apart.
func Report(w io.Writer, recs []Record, opts ReportOptions) error {
	var b strings.Builder

	against := cmp.Or(opts.Against, ArmBaseline)

	tasks := taskIDs(recs, opts.Task)
	if len(tasks) == 0 {
		b.WriteString("no runs recorded")

		if opts.Task != "" {
			b.WriteString(" for task " + opts.Task)
		}

		b.WriteString("\n")
	}

	var all []group

	for i, id := range tasks {
		if i > 0 {
			b.WriteString("\n")
		}

		groups := groupsOf(recs, id)
		all = append(all, groups...)

		reportTask(&b, id, groups, against)
	}

	if opts.Task == "" {
		reportAcross(&b, all, against)
	}

	if _, err := io.WriteString(w, b.String()); err != nil {
		return fmt.Errorf("writing report: %w", err)
	}

	return nil
}

// taskIDs lists the tasks to report in the order they were first run.
func taskIDs(recs []Record, only string) []string {
	var ids []string

	for i := range recs {
		id := recs[i].TaskID
		if (only == "" || id == only) && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}

	return ids
}

func groupsOf(recs []Record, task string) []group {
	var groups []group

	for i := range recs {
		r := &recs[i]
		if r.TaskID != task {
			continue
		}

		key := groupKey{
			task: task, arm: r.Arm, harness: r.Harness, served: r.Served, hash: r.TaskHash,
			build: r.Build, flags: r.Flags,
		}

		at := slices.IndexFunc(groups, func(g group) bool { return g.key == key })
		if at < 0 {
			groups = append(groups, group{key: key})
			at = len(groups) - 1
		}

		groups[at].recs = append(groups[at].recs, *r)
	}

	groups = foldUnserved(groups)

	slices.SortStableFunc(groups, func(a, b group) int {
		return cmp.Or(
			cmp.Compare(a.key.hash, b.key.hash),
			cmp.Compare(a.key.harness, b.key.harness),
			cmp.Compare(a.key.served, b.key.served),
			cmp.Compare(a.key.build, b.key.build),
			cmp.Compare(a.key.flags, b.key.flags),
			cmp.Compare(armRank(a.key.arm), armRank(b.key.arm)),
			cmp.Compare(a.key.arm, b.key.arm),
		)
	})

	return groups
}

// foldUnserved moves runs that died before any model answered into the
// group they would have joined. A timeout or a harness that never started
// names no served model, and left as a group of its own it would vanish
// from the row whose failures it is. It is folded only when exactly one
// group differs from it in the served model alone, since with two there is
// no telling which it belongs to.
func foldUnserved(groups []group) []group {
	out := make([]group, 0, len(groups))

	var unserved []group

	for i := range groups {
		if groups[i].key.served == "" && groups[i].measured() == 0 {
			unserved = append(unserved, groups[i])

			continue
		}

		out = append(out, groups[i])
	}

	for i := range unserved {
		var homes []int

		for j := range out {
			key := out[j].key
			key.served = ""

			if key == unserved[i].key {
				homes = append(homes, j)
			}
		}

		if len(homes) != 1 {
			out = append(out, unserved[i])

			continue
		}

		out[homes[0]].recs = append(out[homes[0]].recs, unserved[i].recs...)
	}

	return out
}

// armRank orders arms the way Arms lists them, with an arm this build does
// not know after them.
func armRank(arm Arm) int {
	if at := slices.Index(Arms(), arm); at >= 0 {
		return at
	}

	return len(armSpecs)
}

const rowFormat = "%-12s %4s %6s %6s %7s %6s %9s %8s %7s %6s %7s\n"

func reportTask(b *strings.Builder, task string, groups []group, against Arm) {
	fmt.Fprintf(b, "task %s\n", task)

	compared := map[Arm]bool{}

	for start := 0; start < len(groups); {
		end := start
		for end < len(groups) && groups[end].key.setup() == groups[start].key.setup() {
			end++
		}

		reportSetup(b, groups[start:end], against, compared)
		start = end
	}

	explainUncompared(b, groups, against, compared)
}

// reportSetup writes one setup's rows and, when the arm compared against
// ran under it, every other arm's ratios to that arm.
func reportSetup(b *strings.Builder, groups []group, against Arm, compared map[Arm]bool) {
	key := groups[0].key
	fmt.Fprintf(b, "setup: %s, served %s, task %s, build %s, flags %s\n",
		key.harness, orDash(key.served), key.hash, orUnknown(key.build), orUnknown(key.flags))
	fmt.Fprintf(b, rowFormat,
		"arm", "runs", "pass", "failed", "timeout", "turns", "cost", "wall", "sweeps", "hints", "denials")

	var base *group

	for i := range groups {
		g := &groups[i]
		if g.key.arm == against {
			base = g
		}

		fmt.Fprintf(b, rowFormat, g.key.arm, strconv.Itoa(len(g.recs)),
			fmt.Sprintf("%d/%d", g.count(func(r *Record) bool { return r.Pass }), len(g.recs)),
			strconv.Itoa(len(g.recs)-g.measured()-g.count(timedOut)), strconv.Itoa(g.count(timedOut)),
			metricTurns.cell(g), metricCost.cell(g), metricWall.cell(g), metricSweeps.cell(g),
			strconv.Itoa(g.total(func(r *Record) int { return r.RouteHints })),
			strconv.Itoa(g.total(func(r *Record) int { return r.RenameDenials })))
	}

	if base == nil {
		return
	}

	for i := range groups {
		if other := &groups[i]; other != base {
			writeRatios(b, base, other)

			compared[other.key.arm] = true
		}
	}
}

func timedOut(r *Record) bool { return r.Stop == StopTimeout }

func orDash(s string) string {
	return cmp.Or(s, "-")
}

// orUnknown labels a build or flags that a record from before either was
// kept does not have.
func orUnknown(s string) string {
	return cmp.Or(s, "unknown")
}

// count is how many of the group's runs satisfy a test.
func (g *group) count(test func(r *Record) bool) int {
	n := 0

	for i := range g.recs {
		if test(&g.recs[i]) {
			n++
		}
	}

	return n
}

// measured is how many of the group's runs the harness completed. Only
// those say what the task costs: a run that timed out or failed stopped
// wherever it happened to be, and what it spent by then, often recorded as
// nothing at all, is not the price of the work.
func (g *group) measured() int {
	return g.count(func(r *Record) bool { return r.Stop == StopCompleted })
}

// total sums a count over the group's runs. Hints and denials are shown
// as totals, because the question they answer is whether a hook fired at
// all, and a median of mostly zeros would hide that.
func (g *group) total(read func(r *Record) int) int {
	sum := 0
	for i := range g.recs {
		sum += read(&g.recs[i])
	}

	return sum
}

// explainUncompared says, for every arm that was never put beside the arm
// compared against, what kept them apart.
func explainUncompared(b *strings.Builder, groups []group, against Arm, compared map[Arm]bool) {
	var (
		arms []Arm
		base *group
	)

	for i := range groups {
		arm := groups[i].key.arm
		if arm == against && base == nil {
			base = &groups[i]
		}

		if !slices.Contains(arms, arm) {
			arms = append(arms, arm)
		}
	}

	switch {
	case len(arms) == 1:
		b.WriteString("no comparison: only one arm has runs\n")

		return
	case base == nil:
		fmt.Fprintf(b, "no comparison: the %s arm has no runs, and every ratio is against it\n", against)

		return
	}

	for i := range groups {
		arm := groups[i].key.arm
		if arm == against || compared[arm] {
			continue
		}

		b.WriteString(refusal(base.key, groups[i].key))

		compared[arm] = true
	}
}

// refusal says which part of the setup kept an arm from being compared.
func refusal(base, other groupKey) string {
	switch {
	case base.harness != other.harness:
		return fmt.Sprintf("no comparison: %s ran under %s and %s under %s\n",
			base.arm, base.harness, other.arm, other.harness)
	case base.served != other.served:
		return fmt.Sprintf("no comparison: %s was served %s and %s was served %s, "+
			"so a difference would measure the model\n",
			base.arm, orDash(base.served), other.arm, orDash(other.served))
	case base.hash != other.hash:
		return fmt.Sprintf("no comparison: the task changed between %s and %s (%s then %s), "+
			"so they answered different questions\n", base.arm, other.arm, base.hash, other.hash)
	case base.build != other.build:
		return fmt.Sprintf("no comparison: %s ran with wavez build %s and %s with build %s, "+
			"so a difference could be the build's\n",
			base.arm, orUnknown(base.build), other.arm, orUnknown(other.build))
	default:
		return fmt.Sprintf("no comparison: %s ran with flags %s and %s with flags %s\n",
			base.arm, orUnknown(base.flags), other.arm, orUnknown(other.flags))
	}
}

func writeRatios(b *strings.Builder, base, other *group) {
	fmt.Fprintf(b, "%s/%s, %d vs %d runs\n", other.key.arm, base.key.arm, len(other.recs), len(base.recs))

	for _, m := range []metric{metricTurns, metricCost, metricWall, metricSweeps} {
		fmt.Fprintf(b, "  %-7s %s\n", m.name, verdict(m.values(base), m.values(other)))
	}
}

// verdict renders one metric's comparison: the ratio of the medians, how
// many runs of each arm measured it, and whether the rank-sum test puts
// the difference outside the noise. It gives no verdict when an arm has
// too few measured runs for any difference to count.
func verdict(base, other []float64) string {
	ratio, why := "    -  ", ""

	switch {
	case len(base) == 0 || len(other) == 0:
	case median(base) == 0:
		why = " (the median it is divided by is zero, so there is no ratio)"
	default:
		ratio = fmt.Sprintf("%5.2fx ", median(other)/median(base))
	}

	text := fmt.Sprintf("%s  n %d vs %d  ", ratio, len(other), len(base))

	if min(len(base), len(other)) < MinRuns {
		return text + fmt.Sprintf("no verdict: fewer than %d measured runs in an arm", MinRuns)
	}

	p := rankSumP(other, base)
	text += fmt.Sprintf("p %.3f  ", p)

	if p <= Alpha {
		return text + "outside the noise" + why
	}

	return text + "inside the noise" + why
}

// metric is one column: how to read it off a record and how to print it.
type metric struct {
	read   func(r *Record) (float64, bool)
	format func(v float64) string
	name   string
}

var (
	metricTurns = metric{name: "turns", read: func(r *Record) (float64, bool) {
		return float64(r.Turns), r.Turns > 0
	}, format: func(v float64) string { return fmt.Sprintf("%g", v) }}

	metricCost = metric{name: "cost", read: func(r *Record) (float64, bool) {
		return r.CostUSD, r.CostUSD > 0
	}, format: func(v float64) string { return fmt.Sprintf("$%.4f", v) }}

	metricWall = metric{name: "wall", read: func(r *Record) (float64, bool) {
		return float64(r.WallMS), true
	}, format: func(v float64) string {
		return (time.Duration(v) * time.Millisecond).Round(time.Second / 10).String() //nolint:mnd // tenths of a second
	}}

	metricSweeps = metric{name: "sweeps", read: func(r *Record) (float64, bool) {
		if r.Transcript == nil {
			return 0, false
		}

		return float64(r.Transcript.Sweeps), true
	}, format: func(v float64) string { return fmt.Sprintf("%g", v) }}
)

// values is the metric over the runs that completed and measured it. A
// harness that reports no turns or cost measured neither, and a run that
// did not complete measured nothing.
func (m *metric) values(g *group) []float64 {
	var values []float64

	for i := range g.recs {
		r := &g.recs[i]
		if r.Stop != StopCompleted {
			continue
		}

		if v, ok := m.read(r); ok {
			values = append(values, v)
		}
	}

	return values
}

// median is the middle of values, which must not be empty.
func median(values []float64) float64 {
	sorted := slices.Sorted(slices.Values(values))

	mid := len(sorted) / 2 //nolint:mnd // the middle
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}

	return (sorted[mid-1] + sorted[mid]) / 2 //nolint:mnd // mean of the middle two
}

func (m *metric) cell(g *group) string {
	values := m.values(g)
	if len(values) == 0 {
		return "-"
	}

	return m.format(median(values))
}
