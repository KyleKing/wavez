package routine_test

import (
	"context"
	"testing"

	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/routine"
)

func BenchmarkCompileSet(b *testing.B) {
	reg := routine.NewRegistry(routine.GateAction(stubGate{}), passing())
	defs := map[string]routine.Definition{"two": def("two", step("a", "ok"), step("b", "ok", "a"))}

	b.ReportAllocs()

	for b.Loop() {
		if _, err := routine.CompileSet(defs, reg, "hash"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRunnerOverhead(b *testing.B) {
	reg := routine.NewRegistry(passing())

	rt, err := routine.Compile(def("three", step("a", "ok"), step("b", "ok", "a"), step("c", "ok", "b")), reg)
	if err != nil {
		b.Fatal(err)
	}

	runner := routine.NewRunner(gate.RealClock{}, gate.NewResourceSet(), nil)

	b.ReportAllocs()

	for b.Loop() {
		if _, err := runner.Run(context.Background(), rt, routine.TriggerManual, routine.Env{}); err != nil {
			b.Fatal(err)
		}
	}
}
