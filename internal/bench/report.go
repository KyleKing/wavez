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

// The thresholds below which a difference between arms is not a result.
// One task measured 40-70% run-to-run variance in turns on an unchanged
// tree, so a ratio has to clear a factor of two, on enough runs, before it
// says anything.
const (
	// MinRuns is how many runs each arm needs.
	MinRuns = 5
	// NoiseLow and NoiseHigh bound the ratios that are inside the noise.
	NoiseLow  = 0.5
	NoiseHigh = 2.0
)

// group is the runs of one task that may be summarized together: same arm,
// same harness, same served model, same task definition.
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
}

// setup is everything two groups must share to be compared.
func (k groupKey) setup() groupKey {
	k.arm = ""

	return k
}

// Report writes, for each task (or the one named), a row per arm and setup
// and then the wavez/baseline ratio of each metric, marked as noise unless
// it clears the thresholds. Arms that were served different models are not
// compared at all.
func Report(w io.Writer, recs []Record, taskID string) error {
	var b strings.Builder

	tasks := taskIDs(recs, taskID)
	if len(tasks) == 0 {
		b.WriteString("no runs recorded")

		if taskID != "" {
			b.WriteString(" for task " + taskID)
		}

		b.WriteString("\n")
	}

	for i, id := range tasks {
		if i > 0 {
			b.WriteString("\n")
		}

		reportTask(&b, id, groupsOf(recs, id))
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

		key := groupKey{task: task, arm: r.Arm, harness: r.Harness, served: r.Served, hash: r.TaskHash}

		at := slices.IndexFunc(groups, func(g group) bool { return g.key == key })
		if at < 0 {
			groups = append(groups, group{key: key})
			at = len(groups) - 1
		}

		groups[at].recs = append(groups[at].recs, *r)
	}

	slices.SortStableFunc(groups, func(a, b group) int {
		return cmp.Or(
			cmp.Compare(a.key.hash, b.key.hash),
			cmp.Compare(a.key.harness, b.key.harness),
			cmp.Compare(a.key.served, b.key.served),
			cmp.Compare(a.key.arm, b.key.arm),
		)
	})

	return groups
}

const rowFormat = "%-9s %-8s %-28s %-13s %4s %6s %6s %9s %8s %7s\n"

func reportTask(b *strings.Builder, task string, groups []group) {
	fmt.Fprintf(b, "task %s\n", task)
	fmt.Fprintf(b, rowFormat,
		"arm", "harness", "served", "task hash", "runs", "pass", "turns", "cost", "wall", "sweeps")

	for i := range groups {
		g := &groups[i]
		passed := 0

		for j := range g.recs {
			if g.recs[j].Pass {
				passed++
			}
		}

		fmt.Fprintf(b, rowFormat, g.key.arm, g.key.harness, orDash(g.key.served), g.key.hash,
			strconv.Itoa(len(g.recs)), fmt.Sprintf("%d/%d", passed, len(g.recs)),
			metricTurns.cell(g), metricCost.cell(g), metricWall.cell(g), metricSweeps.cell(g))
	}

	compareArms(b, groups)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}

	return s
}

// compareArms writes the ratio block for every setup both arms ran, and
// says why there is none when the arms never shared a setup.
func compareArms(b *strings.Builder, groups []group) {
	var baseline, wavez []*group

	for i := range groups {
		switch groups[i].key.arm {
		case ArmBaseline:
			baseline = append(baseline, &groups[i])
		case ArmWavez:
			wavez = append(wavez, &groups[i])
		}
	}

	if len(baseline) == 0 || len(wavez) == 0 {
		b.WriteString("no comparison: only one arm has runs\n")

		return
	}

	compared := false

	for _, base := range baseline {
		for _, wz := range wavez {
			if base.key.setup() == wz.key.setup() {
				writeRatios(b, base, wz)

				compared = true
			}
		}
	}

	if !compared {
		b.WriteString(refusal(baseline[0].key, wavez[0].key))
	}
}

// refusal says which part of the setup kept two arms from being compared.
func refusal(base, wz groupKey) string {
	switch {
	case base.harness != wz.harness:
		return fmt.Sprintf("no comparison: baseline ran under %s and wavez under %s\n", base.harness, wz.harness)
	case base.served != wz.served:
		return fmt.Sprintf("no comparison: baseline was served %s and wavez was served %s, "+
			"so a difference would measure the model\n", orDash(base.served), orDash(wz.served))
	default:
		return fmt.Sprintf("no comparison: the task changed between the arms (%s then %s), "+
			"so they answered different questions\n", base.hash, wz.hash)
	}
}

func writeRatios(b *strings.Builder, base, wz *group) {
	fmt.Fprintf(b, "wavez/baseline on %s %s, %d vs %d runs\n",
		base.key.harness, orDash(base.key.served), len(wz.recs), len(base.recs))

	few := min(len(base.recs), len(wz.recs)) < MinRuns

	for _, m := range []metric{metricTurns, metricCost, metricWall, metricSweeps} {
		fmt.Fprintf(b, "  %-7s %s\n", m.name, verdict(m.median(base), m.median(wz), few))
	}

	if few {
		fmt.Fprintf(b, "  fewer than %d runs per arm: nothing above is a result yet\n", MinRuns)
	}
}

// verdict renders one ratio and says whether it means anything.
func verdict(base, wz sample, few bool) string {
	switch {
	case !base.ok || !wz.ok:
		return "    -   not measured in both arms"
	case base.value == 0 && wz.value == 0:
		return "    -   zero in both arms"
	case base.value == 0:
		return "    -   baseline is zero, so there is no ratio"
	}

	ratio := wz.value / base.value
	text := fmt.Sprintf("%5.2fx  ", ratio)

	switch {
	case few:
		return text + "inside the noise (too few runs)"
	case ratio > NoiseLow && ratio < NoiseHigh:
		return text + fmt.Sprintf("inside the noise (between %gx and %gx)", NoiseLow, NoiseHigh)
	default:
		return text + "outside the noise"
	}
}

// sample is a median that may not exist.
type sample struct {
	value float64
	ok    bool
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

// median is the metric's median over the runs that measured it. A run whose
// harness never started measured nothing and is left out.
func (m *metric) median(g *group) sample {
	var values []float64

	for i := range g.recs {
		r := &g.recs[i]
		if r.Stop == StopHarnessError {
			continue
		}

		if v, ok := m.read(r); ok {
			values = append(values, v)
		}
	}

	if len(values) == 0 {
		return sample{}
	}

	slices.Sort(values)

	mid := len(values) / 2 //nolint:mnd // the middle
	if len(values)%2 == 1 {
		return sample{value: values[mid], ok: true}
	}

	return sample{value: (values[mid-1] + values[mid]) / 2, ok: true} //nolint:mnd // mean of the middle two
}

func (m *metric) cell(g *group) string {
	s := m.median(g)
	if !s.ok {
		return "-"
	}

	return m.format(s.value)
}
