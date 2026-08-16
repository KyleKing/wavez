package gate_test

import (
	"context"
	"path/filepath"
	"testing"

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
		&stubGate{name: "go-test", result: gate.Result{Gate: "go-test", Pass: false, TestCount: 3}},
	}

	runFn := gate.BuildRunFunc(gate.RealClock{}, fakeLineCoverage{}, nil, gates, l, "/repo")

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
	if entries[1].Gate != "go-test" || entries[1].Pass || entries[1].TestCount != 3 {
		t.Errorf("second entry = %+v, want the go-test failure", entries[1])
	}
}
