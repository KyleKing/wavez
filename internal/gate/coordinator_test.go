package gate_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/tool"
)

type stubGate struct {
	name   string
	result gate.Result
}

func (g *stubGate) Name() string      { return g.name }
func (*stubGate) Resources() []string { return nil }

func (g *stubGate) Run(_ context.Context, _ gate.RunContext) (gate.Result, error) {
	return g.result, nil
}

func TestBuildRunFuncLogsEachResult(t *testing.T) {
	t.Parallel()

	logPath := filepath.Join(t.TempDir(), "gate.log")

	l, err := gate.OpenLog(logPath)
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}

	gates := []gate.Gate{
		&stubGate{name: "format", result: gate.Result{Gate: "format", Pass: true}},
		&stubGate{name: "go-test", result: gate.Result{Gate: "go-test", Pass: false, Examined: 3}},
	}

	runFn := gate.BuildRunFunc(gate.RealClock{}, fakeLineCoverage{}, nil, gates, l, "/repo", nil)

	result := runFn(context.Background(), []tool.Change{{Path: "pkg/a.go"}})
	if result.LogError != nil {
		t.Fatalf("LogError = %v, want nil", result.LogError)
	}
	if len(result.Gates) != 2 {
		t.Fatalf("Gates = %+v, want 2 results", result.Gates)
	}

	entries, err := l.Entries()
	if err != nil {
		t.Fatalf("Entries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("logged %d entries, want 2", len(entries))
	}
	if entries[1].Gate != "go-test" || entries[1].Pass || entries[1].Examined != 3 {
		t.Errorf("second entry = %+v, want the go-test failure", entries[1])
	}
}

// recordingGate keeps the selection each batch handed it.
type recordingGate struct{ seen []gate.Selection }

func (*recordingGate) Name() string        { return "recorder" }
func (*recordingGate) Resources() []string { return nil }

func (g *recordingGate) Run(_ context.Context, rc gate.RunContext) (gate.Result, error) {
	g.seen = append(g.seen, rc.Selection)

	return gate.Result{Gate: "recorder", Pass: true}, nil
}

// A selected set that misses a caller is only ever found by a run that does
// not select, so selection cannot narrow forever. The bound is what keeps a
// whole session of green gates from sitting over a build nothing swept.
func TestBuildRunFuncForcesAFullRunOnCadence(t *testing.T) {
	t.Parallel()

	l, err := gate.OpenLog(filepath.Join(t.TempDir(), "gate.log"))
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}

	batch := []tool.Change{{Path: "pkg/a.go", Added: 1, Ranges: []tool.LineRange{{Start: 1, End: 2}}}}
	rec := &recordingGate{}
	clock := newFakeClock(time.Now())
	// Every batch resolves to a covering test, so nothing but the cadence
	// can widen the selection.
	cov := fakeLineCoverage{coverageKey("pkg/a.go", 1, 2): {{TestID: "pkg.TestA"}}}
	runFn := gate.BuildRunFunc(clock, cov, nil, []gate.Gate{rec}, l, "/repo", nil)

	batches := gate.DefaultCadence.MaxSelectivePasses + 1
	for range batches {
		runFn(context.Background(), batch)
	}

	if len(rec.seen) != batches {
		t.Fatalf("saw %d selections, want %d", len(rec.seen), batches)
	}

	for i, s := range rec.seen[:batches-1] {
		if s.Level != gate.LevelLine {
			t.Errorf("selection %d was %q, want it still narrowed", i, s.Level)
		}
	}

	last := rec.seen[batches-1]
	if last.Level != gate.LevelPackage || len(last.Packages) != 1 || last.Packages[0] != "./..." {
		t.Errorf("the %dth selection = %+v, want a sweep of the module", batches, last)
	}
}

// The interval is the other half, and it fires without any batch count.
func TestBuildRunFuncForcesAFullRunAfterTheInterval(t *testing.T) {
	t.Parallel()

	l, err := gate.OpenLog(filepath.Join(t.TempDir(), "gate.log"))
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}

	batch := []tool.Change{{Path: "pkg/a.go", Added: 1, Ranges: []tool.LineRange{{Start: 1, End: 2}}}}
	rec := &recordingGate{}
	clock := newFakeClock(time.Now())
	cov := fakeLineCoverage{coverageKey("pkg/a.go", 1, 2): {{TestID: "pkg.TestA"}}}
	runFn := gate.BuildRunFunc(clock, cov, nil, []gate.Gate{rec}, l, "/repo", nil)

	runFn(context.Background(), batch)
	clock.Advance(gate.DefaultCadence.MaxInterval + time.Second)
	runFn(context.Background(), batch)

	if rec.seen[0].Level != gate.LevelLine {
		t.Errorf("first selection = %q, want it narrowed", rec.seen[0].Level)
	}

	if rec.seen[1].Level != gate.LevelPackage {
		t.Errorf("second selection = %q, want the interval to have widened it", rec.seen[1].Level)
	}
}
