// Command progressestimate replays wavez thread logs and scores how well a
// few estimators predict a run's remaining wall clock at each turn
// boundary, which is the question DESIGN.md's progress line hangs on.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type event struct {
	At    time.Time      `json:"at"`
	Kind  string         `json:"kind"`
	State string         `json:"state"`
	Role  string         `json:"role"`
	Text  string         `json:"text"`
	Tool  string         `json:"tool"`
	Extra map[string]any `json:"detail"`
}

// run is one thread's timeline: when it started, when it stopped, and the
// wall-clock offset of every turn boundary in between.
type run struct {
	name  string
	turns []time.Duration
	total time.Duration
	edits int
}

func main() {
	minTurns := flag.Int("min-turns", 2, "skip runs with fewer turn boundaries")
	whole := flag.Bool("whole-thread", false,
		"score whole threads instead of one run per user prompt, which counts human think time as run time")
	flag.Parse()

	var runs []run
	for _, dir := range flag.Args() {
		files, _ := filepath.Glob(filepath.Join(dir, "*.jsonl"))
		for _, f := range files {
			for _, r := range load(f, *whole) {
				if len(r.turns) >= *minTurns {
					runs = append(runs, r)
				}
			}
		}
	}
	if len(runs) == 0 {
		fmt.Fprintln(os.Stderr, "no runs with enough turns; pass one or more .wavez/threads directories")
		os.Exit(1)
	}

	fmt.Printf("%d runs, %d turn boundaries\n\n", len(runs), boundaries(runs))
	mae, med, hit := scoreNextTurn(runs)
	fmt.Printf("next turn from own mean gap: MAE %.1fs median %.1fs within 2x %.0f%%\n\n", mae, med, hit*100)
	fmt.Printf("%-28s %10s %10s %8s\n", "estimator", "MAE (s)", "median (s)", "within 2x")
	for _, e := range estimators() {
		mae, med, hit := score(runs, e.fn)
		fmt.Printf("%-28s %10.1f %10.1f %7.0f%%\n", e.name, mae, med, hit*100)
	}
}

// load reads one thread log into the runs it holds. A run is one user
// prompt and the work it caused: it starts at a user event and ends at the
// last event before the next one, so the minutes a thread spends waiting
// for its human are not counted as work a progress line could predict.
// With whole set, a thread is one run, which is what the first pass
// measured.
//
// A turn boundary is an agent note carrying usage, which is where one model
// call ended; a run ends at its last event, whatever state it reached.
func load(path string, whole bool) []run {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var (
		runs []run
		cur  run
		open bool
	)

	base := filepath.Base(path)

	closeRun := func(end time.Time, start time.Time) {
		if !open {
			return
		}
		cur.total = end.Sub(start)
		if cur.total > 0 {
			runs = append(runs, cur)
		}
		open = false
	}

	var start, last time.Time

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)

	for sc.Scan() {
		var ev event
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}

		if ev.Kind == "user" && !whole {
			closeRun(last, start)
			cur = run{name: fmt.Sprintf("%s#%d", base, len(runs)+1)}
			start = ev.At
			open = true
		}

		if !open {
			if start.IsZero() {
				start = ev.At
				cur = run{name: base}
				open = true
			} else {
				continue
			}
		}

		last = ev.At

		if ev.Kind == "agent" && ev.Role == "note" && ev.Extra["usage"] != nil {
			cur.turns = append(cur.turns, ev.At.Sub(start))
		}

		if ev.Kind == "tool" && (ev.Tool == "str_replace" || ev.Tool == "write") {
			cur.edits++
		}
	}

	closeRun(last, start)

	return runs
}

func boundaries(runs []run) int {
	n := 0
	for _, r := range runs {
		n += len(r.turns)
	}

	return n
}

// estimator predicts the remaining wall clock at boundary i of run r, given
// every other run as history. It never sees r's own future.
type estimator struct {
	name string
	fn   func(r run, i int, history []run) time.Duration
}

func estimators() []estimator {
	return []estimator{
		{"elapsed doubles", func(r run, i int, _ []run) time.Duration { return r.turns[i] }},
		{"own mean turn x 3", func(r run, i int, _ []run) time.Duration {
			return 3 * r.turns[i] / time.Duration(i+1)
		}},
		{"history median total", func(r run, i int, h []run) time.Duration {
			return max(median(totals(h))-r.turns[i], 0)
		}},
		{"history conditional median", func(r run, i int, h []run) time.Duration {
			var longer []time.Duration
			for _, o := range h {
				if o.total > r.turns[i] {
					longer = append(longer, o.total)
				}
			}
			if len(longer) == 0 {
				return r.turns[i] / time.Duration(i+1)
			}

			return median(longer) - r.turns[i]
		}},
		{"same shape conditional", func(r run, i int, h []run) time.Duration {
			var longer []time.Duration
			for _, o := range h {
				if (o.edits > 0) == (r.edits > 0) && o.total > r.turns[i] {
					longer = append(longer, o.total)
				}
			}
			if len(longer) == 0 {
				return r.turns[i] / time.Duration(i+1)
			}

			return median(longer) - r.turns[i]
		}},
	}
}

// score is leave-one-run-out: every boundary of every run is predicted
// with the other runs as history.
func score(runs []run, fn func(run, int, []run) time.Duration) (mae, med, hit float64) {
	var errs []float64
	hits := 0
	for k, r := range runs {
		history := append(append([]run(nil), runs[:k]...), runs[k+1:]...)
		for i := range r.turns {
			actual := r.total - r.turns[i]
			pred := fn(r, i, history)
			e := math.Abs(pred.Seconds() - actual.Seconds())
			errs = append(errs, e)
			if actual > 0 && pred >= actual/2 && pred <= actual*2 {
				hits++
			}
		}
	}
	sort.Float64s(errs)
	sum := 0.0
	for _, e := range errs {
		sum += e
	}

	return sum / float64(len(errs)), errs[len(errs)/2], float64(hits) / float64(len(errs))
}

// scoreNextTurn asks the easier question a progress line can also ask: how
// long until this turn ends, predicted from the mean gap so far.
func scoreNextTurn(runs []run) (mae, med, hit float64) {
	var errs []float64

	hits := 0

	for _, r := range runs {
		for i := 0; i+1 < len(r.turns); i++ {
			actual := (r.turns[i+1] - r.turns[i]).Seconds()
			pred := (r.turns[i] / time.Duration(i+1)).Seconds()
			errs = append(errs, math.Abs(pred-actual))

			if actual > 0 && pred >= actual/2 && pred <= actual*2 {
				hits++
			}
		}
	}

	if len(errs) == 0 {
		return 0, 0, 0
	}

	sort.Float64s(errs)

	sum := 0.0
	for _, e := range errs {
		sum += e
	}

	return sum / float64(len(errs)), errs[len(errs)/2], float64(hits) / float64(len(errs))
}

func totals(runs []run) []time.Duration {
	out := make([]time.Duration, len(runs))
	for i, r := range runs {
		out[i] = r.total
	}

	return out
}

func median(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })

	return s[len(s)/2]
}
