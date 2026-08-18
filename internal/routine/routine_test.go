package routine_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/routine"
	"github.com/kyleking/wavez/internal/tool"
)

// recorder collects the step names an action ran, in completion order, so a
// test can assert dependency order without a sleep.
type recorder struct {
	names []string
	mu    sync.Mutex
}

func (r *recorder) note(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.names = append(r.names, name)
}

func (r *recorder) seen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]string(nil), r.names...)
}

// stubAction registers an action whose handler is supplied per step through
// a "label" parameter, which is how a test names the step it is watching.
func stubAction(name string, resources []string, run func(ctx context.Context, label string) (routine.Outcome, error)) routine.Action {
	return routine.Action{
		Name: name,
		Bind: func(params map[string]any) (routine.Bound, error) {
			label, ok := params["label"].(string)
			if !ok {
				return routine.Bound{}, errors.New("label is required")
			}

			return routine.Bound{
				Resources: resources,
				Run: func(ctx context.Context, _ routine.Env) (routine.Outcome, error) {
					return run(ctx, label)
				},
			}, nil
		},
	}
}

func step(name, action string, parents ...string) routine.StepDef {
	return routine.StepDef{
		Name:    name,
		Action:  action,
		Params:  map[string]any{"label": name},
		Parents: parents,
	}
}

func def(name string, steps ...routine.StepDef) routine.Definition {
	return routine.Definition{
		Name:     name,
		Triggers: []routine.Trigger{routine.TriggerManual},
		Steps:    steps,
		Enabled:  true,
	}
}

func passing() routine.Action {
	return stubAction("ok", nil, func(context.Context, string) (routine.Outcome, error) {
		return routine.Outcome{Pass: true, Examined: 1}, nil
	})
}

func TestCompile_RejectsRoutinesTheRunnerCouldNeverExecute(t *testing.T) {
	t.Parallel()

	reg := routine.NewRegistry(passing(), routine.RunAction(t.TempDir()))

	tests := []struct {
		want error
		name string
		def  routine.Definition
	}{
		{name: "no steps", def: def("empty"), want: routine.ErrNoSteps},
		{name: "duplicate step", def: def("dup", step("a", "ok"), step("a", "ok")), want: routine.ErrDuplicateStep},
		{name: "unknown parent", def: def("orphan", step("a", "ok", "ghost")), want: routine.ErrUnknownParent},
		{name: "cycle", def: def("loop", step("a", "ok", "b"), step("b", "ok", "a")), want: routine.ErrCyclicDAG},
		{name: "unknown action", def: def("missing", step("a", "nope")), want: routine.ErrUnknownAction},
		{
			name: "run without argv",
			def:  routine.Definition{Name: "r", Enabled: true, Steps: []routine.StepDef{{Name: "a", Action: "run"}}},
			want: routine.ErrMissingParam,
		},
		{
			name: "run with a non-string argv",
			def: routine.Definition{Name: "r", Enabled: true, Steps: []routine.StepDef{
				{Name: "a", Action: "run", Params: map[string]any{"argv": []any{1}}},
			}},
			want: routine.ErrParamType,
		},
		{
			name: "run with an unknown parameter",
			def: routine.Definition{Name: "r", Enabled: true, Steps: []routine.StepDef{
				{Name: "a", Action: "run", Params: map[string]any{"argv": []any{"true"}, "shell": "bash"}},
			}},
			want: routine.ErrUnknownParam,
		},
		{
			name: "run outside the project",
			def: routine.Definition{Name: "r", Enabled: true, Steps: []routine.StepDef{
				{Name: "a", Action: "run", Params: map[string]any{"argv": []any{"true"}, "dir": "../.."}},
			}},
			want: routine.ErrDirEscapes,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := routine.Compile(tc.def, reg)
			require.ErrorIs(t, err, tc.want)
		})
	}
}

func TestRunner_RunsWavesInOrderAndSkipsChildrenOfAFailedStep(t *testing.T) {
	t.Parallel()

	var rec recorder

	reg := routine.NewRegistry(
		stubAction("ok", nil, func(_ context.Context, label string) (routine.Outcome, error) {
			rec.note(label)

			return routine.Outcome{Pass: true, Examined: 1}, nil
		}),
		stubAction("bad", nil, func(_ context.Context, label string) (routine.Outcome, error) {
			rec.note(label)

			return routine.Outcome{Failures: []gate.TrimmedFailure{{Test: label}}}, nil
		}),
	)

	// root fans out to two independent steps; only "after-bad" is skipped.
	rt, err := routine.Compile(def("dag",
		step("root", "ok"),
		step("bad", "bad", "root"),
		step("good", "ok", "root"),
		step("after-bad", "ok", "bad"),
	), reg)
	require.NoError(t, err)

	runner := routine.NewRunner(gate.RealClock{}, gate.NewResourceSet(), nil)

	got, err := runner.Run(context.Background(), rt, routine.TriggerManual, routine.Env{})
	require.NoError(t, err)

	assert.False(t, got.Pass)
	assert.Equal(t, "root", rec.seen()[0], "the root step runs before the wave that depends on it")

	status := map[string]routine.Status{}
	for _, s := range got.Steps {
		status[s.Name] = s.Status
	}

	assert.Equal(t, routine.StatusPass, status["root"])
	assert.Equal(t, routine.StatusFail, status["bad"])
	assert.Equal(t, routine.StatusPass, status["good"], "a sibling of a failed step still runs")
	assert.Equal(t, routine.StatusSkipped, status["after-bad"])
	assert.NotContains(t, rec.seen(), "after-bad")
}

func TestRunner_SerializesStepsSharingAResourceKey(t *testing.T) {
	t.Parallel()

	var (
		mu      sync.Mutex
		live    int
		maxLive int
	)

	reg := routine.NewRegistry(stubAction("busy", []string{"go-test"}, func(context.Context, string) (routine.Outcome, error) {
		mu.Lock()
		live++
		maxLive = max(maxLive, live)
		mu.Unlock()

		defer func() {
			mu.Lock()
			live--
			mu.Unlock()
		}()

		return routine.Outcome{Pass: true, Examined: 1}, nil
	}))

	rt, err := routine.Compile(def("parallel", step("a", "busy"), step("b", "busy"), step("c", "busy")), reg)
	require.NoError(t, err)

	runner := routine.NewRunner(gate.RealClock{}, gate.NewResourceSet(), nil)

	_, err = runner.Run(context.Background(), rt, routine.TriggerManual, routine.Env{})
	require.NoError(t, err)
	assert.Equal(t, 1, maxLive, "steps sharing a resource key must not overlap")
}

func TestRunner_CancelInProgressTakesTheKeyFromTheRunningInstance(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})

	reg := routine.NewRegistry(
		stubAction("block", nil, func(ctx context.Context, _ string) (routine.Outcome, error) {
			close(started)
			<-ctx.Done()

			return routine.Outcome{}, ctx.Err()
		}),
		passing(),
	)

	holding := def("holding", step("hold", "block"))
	holding.ConcurrencyKey = "shared"

	arriving := def("arriving", step("quick", "ok"))
	arriving.ConcurrencyKey = "shared"
	arriving.Concurrency = routine.CancelInProgress

	held, err := routine.Compile(holding, reg)
	require.NoError(t, err)

	incoming, err := routine.Compile(arriving, reg)
	require.NoError(t, err)

	runner := routine.NewRunner(gate.RealClock{}, gate.NewResourceSet(), nil)

	first := make(chan routine.RunRecord, 1)

	go func() {
		rec, _ := runner.Run(context.Background(), held, routine.TriggerManual, routine.Env{})
		first <- rec
	}()

	<-started

	second, err := runner.Run(context.Background(), incoming, routine.TriggerManual, routine.Env{})
	require.NoError(t, err)
	assert.True(t, second.Pass, "the arriving run takes the key and completes")

	canceled := <-first
	require.Len(t, canceled.Steps, 1)
	assert.Equal(t, routine.StatusCanceled, canceled.Steps[0].Status)
}

func TestRunAction_TrimsFailureToTheChangedFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	script := filepath.Join(root, "fail.sh")
	require.NoError(t, os.WriteFile(script,
		[]byte("#!/bin/sh\necho 'noise from elsewhere.go:9'\necho 'internal/lease.go:12: broke'\nexit 1\n"), 0o700))

	reg := routine.NewRegistry(routine.RunAction(root))

	rt, err := routine.Compile(routine.Definition{
		Name: "check", Enabled: true,
		Steps: []routine.StepDef{{Name: "run", Action: "run", Params: map[string]any{"argv": []any{script}}}},
	}, reg)
	require.NoError(t, err)

	runner := routine.NewRunner(gate.RealClock{}, gate.NewResourceSet(), nil)

	got, err := runner.Run(context.Background(), rt, routine.TriggerChange, routine.Env{
		Root:    root,
		Changes: []tool.Change{{Path: "internal/lease.go"}},
	})
	require.NoError(t, err)

	require.Len(t, got.Steps, 1)
	assert.Equal(t, routine.StatusFail, got.Steps[0].Status)
	require.Len(t, got.Steps[0].Failures, 1)
	assert.Equal(t, []string{"internal/lease.go:12: broke"}, got.Steps[0].Failures[0].Frames,
		"only the lines touching a changed file survive trimming")
}

func TestSet_BuiltinsMergeAndDisablingOneDropsItsGate(t *testing.T) {
	t.Parallel()

	reg := routine.NewRegistry(routine.GateAction(stubGate{}), passing())

	defs := map[string]routine.Definition{
		routine.BuiltinName("format"): {Enabled: false},
		"nightly": {
			Triggers: []routine.Trigger{routine.TriggerSchedule},
			Steps:    []routine.StepDef{step("a", "ok")},
			Interval: time.Hour,
			Enabled:  true,
		},
	}

	set, err := routine.CompileSet(defs, reg, "hash-1")
	require.NoError(t, err)

	assert.Equal(t, []string{"format"}, set.DisabledGates())

	names := make([]string, 0, len(set.All()))
	for _, rt := range set.All() {
		names = append(names, rt.Name)
	}

	assert.Equal(t, []string{routine.BuiltinName("format"), "nightly"}, names,
		"only built-ins whose gate action is registered survive")

	nightly, ok := set.Get("nightly")
	require.True(t, ok)
	assert.True(t, nightly.Triggered(routine.TriggerSchedule))
	assert.False(t, nightly.Triggered(routine.TriggerChange))
}

func TestCache_RecompilesOnlyWhenTheConfigHashDrifts(t *testing.T) {
	t.Parallel()

	reg := routine.NewRegistry(passing())
	defs := map[string]routine.Definition{"one": def("one", step("a", "ok"))}

	var cache routine.Cache

	first, err := cache.Compiled("hash-1", defs, reg)
	require.NoError(t, err)

	again, err := cache.Compiled("hash-1", defs, reg)
	require.NoError(t, err)
	assert.Same(t, first, again, "an unchanged config hash reuses the compiled DAG")

	drifted, err := cache.Compiled("hash-2", defs, reg)
	require.NoError(t, err)
	assert.NotSame(t, first, drifted, "a drifted hash recompiles rather than patching")
}

func TestHashFile_ReportsAbsentConfigAsNoHash(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	empty, err := routine.HashFile(filepath.Join(root, ".wavez.pkl"))
	require.NoError(t, err)
	assert.Empty(t, empty)

	path := filepath.Join(root, ".wavez.pkl")
	require.NoError(t, os.WriteFile(path, []byte("routines {}"), 0o600))

	hashed, err := routine.HashFile(path)
	require.NoError(t, err)
	assert.NotEmpty(t, hashed)
}

// stubGate is the smallest gate.Gate a GateAction can wrap.
type stubGate struct{}

func (stubGate) Name() string        { return "format" }
func (stubGate) Resources() []string { return []string{"worktree"} }
func (stubGate) Run(context.Context, gate.RunContext) (gate.Result, error) {
	return gate.Result{Gate: "format", Pass: true, Examined: 1}, nil
}
