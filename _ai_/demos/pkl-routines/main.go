package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"sort"
	"time"

	"github.com/apple/pkl-go/pkl"
)

type Step struct {
	Name              string   `pkl:"name"`
	Cmd               []string `pkl:"cmd"`
	Needs             []string `pkl:"needs"`
	ConcurrencyKey    *string  `pkl:"concurrencyKey"`
	CancelInProgress  bool     `pkl:"cancelInProgress"`
}

type Routine struct {
	Triggers []string `pkl:"triggers"`
	Steps    []*Step  `pkl:"steps"`
	Locks    []string `pkl:"locks"`
}

type Wavez struct {
	Routines map[string]*Routine `pkl:"routines"`
}

func main() {
	ctx := context.Background()
	modPath := "file:///Users/kyleking/Developer/kyleking/wavez/_ai_/demos/pkl-routines/.wavez.pkl"

	// cold: full evaluator spawn + eval
	coldStart := time.Now()
	evaluator, err := pkl.NewEvaluator(ctx, pkl.PreconfiguredOptions)
	if err != nil {
		fmt.Fprintln(os.Stderr, "new evaluator:", err)
		os.Exit(1)
	}
	var result Wavez
	if err := evaluator.EvaluateModule(ctx, pkl.FileSource(modPath[len("file://"):]), &result); err != nil {
		fmt.Fprintln(os.Stderr, "cold eval:", err)
		os.Exit(1)
	}
	coldElapsed := time.Since(coldStart)

	if len(result.Routines) != 3 {
		fmt.Fprintln(os.Stderr, "unexpected routine count:", len(result.Routines))
		os.Exit(1)
	}

	// warm: reuse same evaluator, 20 iterations
	const iterations = 20
	warmTimes := make([]time.Duration, 0, iterations)
	for i := 0; i < iterations; i++ {
		start := time.Now()
		var r Wavez
		if err := evaluator.EvaluateModule(ctx, pkl.FileSource(modPath[len("file://"):]), &r); err != nil {
			fmt.Fprintln(os.Stderr, "warm eval:", err)
			os.Exit(1)
		}
		warmTimes = append(warmTimes, time.Since(start))
	}
	if err := evaluator.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "close:", err)
	}

	sort.Slice(warmTimes, func(i, j int) bool { return warmTimes[i] < warmTimes[j] })
	var sum time.Duration
	for _, d := range warmTimes {
		sum += d
	}
	avg := sum / time.Duration(len(warmTimes))
	median := warmTimes[len(warmTimes)/2]
	min := warmTimes[0]
	max := warmTimes[len(warmTimes)-1]

	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	fmt.Println("metric,value")
	fmt.Printf("cold_eval,%s\n", coldElapsed)
	fmt.Printf("warm_avg,%s\n", avg)
	fmt.Printf("warm_median,%s\n", median)
	fmt.Printf("warm_min,%s\n", min)
	fmt.Printf("warm_max,%s\n", max)
	fmt.Printf("go_heap_alloc_kb,%d\n", mem.Alloc/1024)
	fmt.Printf("go_sys_kb,%d\n", mem.Sys/1024)

	names := make([]string, 0, len(result.Routines))
	for k := range result.Routines {
		names = append(names, k)
	}
	sort.Strings(names)
	fmt.Println("routines:", names)
}
