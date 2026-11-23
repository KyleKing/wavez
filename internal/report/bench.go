package report

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strconv"
	"time"

	"github.com/kyleking/wavez/internal/bench"
)

// BenchPage is the benchmark records laid out one task at a time, each arm
// beside the others.
type BenchPage struct {
	// Source names the records file.
	Source string
	// Runs is how many records the page covers.
	Runs  string
	Tasks []BenchTask
}

// BenchTask is every arm that ran one task.
type BenchTask struct {
	ID   string
	Arms []BenchArm
}

// BenchArm is the runs of one task under one arm, served by one model on
// one definition of the task. Its numbers are medians over the runs the
// harness completed.
type BenchArm struct {
	Arm    string
	Model  string
	Runs   string
	Passed string
	// PassClass is the CSS class for whether every run passed.
	PassClass string
	Turns     string
	Cost      string
	Wall      string
	Sweeps    string
	// TurnsWidth, CostWidth, and WallWidth size each bar against the
	// largest value among the task's arms.
	TurnsWidth string
	CostWidth  string
	WallWidth  string
	// Thin is true when the arm has too few runs for a difference to
	// mean anything.
	Thin bool
}

// armKey is what runs must share to be summarized together.
type armKey struct {
	arm    bench.Arm
	served string
	hash   string
}

// armRuns collects one arm's measurements.
type armRuns struct {
	key    armKey
	turns  []float64
	cost   []float64
	wall   []float64
	sweeps []float64
	runs   int
	passed int
}

// BuildBench lays the records out as a page.
func BuildBench(source string, recs []bench.Record) BenchPage {
	page := BenchPage{Source: source, Runs: number(len(recs))}

	var ids []string

	for i := range recs {
		if !slices.Contains(ids, recs[i].TaskID) {
			ids = append(ids, recs[i].TaskID)
		}
	}

	for _, id := range ids {
		page.Tasks = append(page.Tasks, benchTask(id, recs))
	}

	return page
}

func benchTask(id string, recs []bench.Record) BenchTask {
	var arms []armRuns

	for i := range recs {
		rec := &recs[i]
		if rec.TaskID != id {
			continue
		}

		key := armKey{arm: rec.Arm, served: rec.Served, hash: rec.TaskHash}

		at := slices.IndexFunc(arms, func(a armRuns) bool { return a.key == key })
		if at < 0 {
			arms = append(arms, armRuns{key: key})
			at = len(arms) - 1
		}

		arms[at].add(rec)
	}

	slices.SortStableFunc(arms, func(x, y armRuns) int {
		return cmp.Or(
			cmp.Compare(x.key.served, y.key.served),
			cmp.Compare(x.key.hash, y.key.hash),
			cmp.Compare(slices.Index(bench.Arms(), x.key.arm), slices.Index(bench.Arms(), y.key.arm)),
		)
	})

	var most [3]float64

	for i := range arms {
		for j, values := range [][]float64{arms[i].turns, arms[i].cost, arms[i].wall} {
			most[j] = max(most[j], median(values))
		}
	}

	task := BenchTask{ID: id}

	for i := range arms {
		task.Arms = append(task.Arms, arms[i].view(most))
	}

	return task
}

func (a *armRuns) add(rec *bench.Record) {
	a.runs++

	if rec.Pass {
		a.passed++
	}

	if rec.Error != "" {
		return
	}

	a.turns = append(a.turns, float64(rec.Turns))
	a.cost = append(a.cost, rec.CostUSD)
	a.wall = append(a.wall, float64(rec.WallMS))

	if rec.Transcript != nil {
		a.sweeps = append(a.sweeps, float64(rec.Transcript.Sweeps))
	}
}

func (a *armRuns) view(most [3]float64) BenchArm {
	arm := BenchArm{
		Arm:       string(a.key.arm),
		Model:     cmp.Or(a.key.served, NotRecorded),
		Runs:      number(a.runs),
		Passed:    number(a.passed) + " of " + number(a.runs),
		PassClass: "pass-some",
		Turns:     NotRecorded,
		Cost:      NotRecorded,
		Wall:      NotRecorded,
		Sweeps:    NotRecorded,
		Thin:      a.runs < bench.MinRuns,
	}

	if a.passed == a.runs {
		arm.PassClass = "pass-all"
	}

	if len(a.turns) > 0 {
		arm.Turns = strconv.FormatFloat(median(a.turns), 'f', -1, 64)
		arm.Cost = dollars(median(a.cost))
		arm.Wall = span(time.Duration(median(a.wall)) * time.Millisecond)
		arm.TurnsWidth = width(median(a.turns), most[0])
		arm.CostWidth = width(median(a.cost), most[1])
		arm.WallWidth = width(median(a.wall), most[2])
	}

	if len(a.sweeps) > 0 {
		arm.Sweeps = strconv.FormatFloat(median(a.sweeps), 'f', -1, 64)
	}

	return arm
}

// median is the middle value, or the mean of the middle two.
func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}

	sorted := slices.Sorted(slices.Values(values))
	mid := len(sorted) / 2 //nolint:mnd // the middle

	if len(sorted)%2 == 1 {
		return sorted[mid]
	}

	return (sorted[mid-1] + sorted[mid]) / 2 //nolint:mnd // the mean of the middle two
}

// width is a value's share of the largest, as a CSS percentage.
func width(value, most float64) string {
	if most <= 0 {
		return "0%"
	}

	return strconv.FormatFloat(value*hundred/most, 'f', 1, 64) + "%"
}

// HTML writes the benchmark page as one self-contained document.
func (p *BenchPage) HTML(w io.Writer) error {
	if err := benchTemplate.Execute(w, p); err != nil {
		return fmt.Errorf("rendering the benchmark page: %w", err)
	}

	return nil
}
