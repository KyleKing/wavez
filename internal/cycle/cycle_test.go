package cycle_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/condition"
	"github.com/kyleking/wavez/internal/cycle"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/tool"
)

// phaseScript is what a fake phase does when it runs: what it changed, what
// it recorded, and how its loop ended. It stands in for the model so the
// tests measure the harness's own decisions and spend no model runs.
type phaseScript struct {
	reason     string
	changes    []tool.Change
	hypotheses []cycle.Hypothesis
	sweeps     []cycle.Sweep
	complete   bool
}

type scriptDriver struct {
	scripts map[string][]phaseScript
	calls   map[string]int
	prompts []string
}

func newDriver(scripts map[string][]phaseScript) *scriptDriver {
	return &scriptDriver{scripts: scripts, calls: map[string]int{}}
}

func (d *scriptDriver) Drive(_ context.Context, a cycle.Attempt) (cycle.PhaseResult, error) {
	d.prompts = append(d.prompts, a.Prompt)

	runs := d.scripts[a.Phase.Name]

	script := phaseScript{}
	if n := d.calls[a.Phase.Name]; len(runs) > 0 {
		script = runs[min(n, len(runs)-1)]
	}

	d.calls[a.Phase.Name]++

	for _, h := range script.hypotheses {
		if err := a.Ledger.RecordHypothesis(h); err != nil {
			return cycle.PhaseResult{}, fmt.Errorf("scripted hypothesis: %w", err)
		}
	}

	for _, s := range script.sweeps {
		if err := a.Ledger.RecordSweep(s); err != nil {
			return cycle.PhaseResult{}, fmt.Errorf("scripted sweep: %w", err)
		}
	}

	return cycle.PhaseResult{
		Changes:  script.changes,
		Complete: script.complete,
		Stop:     condition.Met("stub", script.reason),
		Turns:    1,
	}, nil
}

// fakeProber answers one probe per call, so the same test can fail in the
// reproduce phase and pass in the fix phase.
type fakeProber struct {
	rounds [][]cycle.Observation
	calls  int
}

func (p *fakeProber) Probe(context.Context, string, []tool.Change) ([]cycle.Observation, error) {
	if p.calls >= len(p.rounds) {
		return nil, nil
	}

	out := p.rounds[p.calls]
	p.calls++

	return out, nil
}

type fakeSweeper struct{ hits []cycle.Hit }

func (s fakeSweeper) Sweep(context.Context, string, cycle.Sweep) ([]cycle.Hit, error) {
	return s.hits, nil
}

// recordLog collects what the Runner wrote to the thread it belongs to.
type recordLog struct {
	events []event.Event
	seq    uint64
}

func (l *recordLog) Append(ev event.Event) (uint64, error) {
	l.seq++
	l.events = append(l.events, ev)

	return l.seq, nil
}

const leaseTest = "TestLeaseTTL"

func failing() []cycle.Observation {
	return []cycle.Observation{{Package: "./internal/lease", Test: leaseTest, Failed: true, Detail: "want 5m, got 0"}}
}

func passing() []cycle.Observation {
	return []cycle.Observation{{Package: "./internal/lease", Test: leaseTest, Detail: "passed"}}
}

func changed(path string) []tool.Change {
	return []tool.Change{{Path: path, Added: 12, Ranges: []tool.LineRange{{Start: 1, End: 12}}}}
}

// The fix cycle drives all three phases only when the harness observes each
// exit condition itself: a failing test, then that test passing with green
// gates, then a sweep with nothing left untriaged.
func TestRunner_FixCycleAdvancesOnEveryCondition(t *testing.T) {
	t.Parallel()

	driver := newDriver(map[string][]phaseScript{
		"reproduce": {{
			changes: changed("internal/lease/lease_test.go"),
			hypotheses: []cycle.Hypothesis{{
				Cause: "the TTL is never read", Experiment: "logged the parsed value",
				Observation: "0s on every call", Verdict: "confirmed",
			}},
		}},
		"fix":        {{changes: changed("internal/lease/lease.go"), complete: true}},
		"generalize": {{sweeps: []cycle.Sweep{{Pattern: "time.Duration($X)", Language: "go"}}, complete: true}},
	})

	prober := &fakeProber{rounds: [][]cycle.Observation{failing(), passing()}}
	log := &recordLog{}
	runner := cycle.NewRunner(t.TempDir(), driver, log)

	out, err := runner.Run(t.Context(), cycle.Fix(cycle.Checks{Prober: prober, Sweeper: fakeSweeper{}}),
		"make the lease TTL configurable")
	require.NoError(t, err)

	assert.Equal(t, cycle.StopComplete, out.Stop)
	assert.Len(t, out.Phases, 3)
	assert.True(t, out.Verdict.Holds)

	for _, p := range out.Phases {
		assert.Equal(t, 1, p.Attempts, "phase %s retried", p.Phase)
		assert.True(t, p.Verdict.Holds, "phase %s did not hold", p.Phase)
	}

	assert.Len(t, out.Changes, 2)
	assert.Len(t, out.Ledger.Hypotheses, 1)
}

// A phase whose Condition does not hold ends the cycle with that reason,
// never as complete, however the phase's own loop reported itself.
func TestRunner_RefusesAPhaseWhoseConditionDoesNotHold(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		scripts map[string][]phaseScript
		prober  *fakeProber
		phase   string
		reason  string
	}{
		{
			name:    "reproduce produced no failing artifact",
			scripts: map[string][]phaseScript{"reproduce": {{changes: changed("internal/lease/lease.go")}}},
			prober:  &fakeProber{},
			phase:   "reproduce",
			reason:  "declares no test",
		},
		{
			name: "reproduce wrote a test that already passes",
			scripts: map[string][]phaseScript{
				"reproduce": {{changes: changed("internal/lease/lease_test.go")}},
			},
			prober: &fakeProber{rounds: [][]cycle.Observation{passing(), passing()}},
			phase:  "reproduce",
			reason: "nothing is reproduced",
		},
		{
			name: "the fix phase's test survives its own revert",
			scripts: map[string][]phaseScript{
				"reproduce": {{changes: changed("internal/lease/lease_test.go")}},
				"fix": {{
					changes:  changed("internal/lease/lease.go"),
					complete: false,
					reason:   "verification failed after 2 round(s): survives-revert",
				}},
			},
			prober: &fakeProber{rounds: [][]cycle.Observation{
				failing(), passing(), passing(),
			}},
			phase:  "fix",
			reason: "survives-revert",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			driver := newDriver(tt.scripts)
			runner := cycle.NewRunner(t.TempDir(), driver, &recordLog{})

			out, err := runner.Run(t.Context(),
				cycle.Fix(cycle.Checks{Prober: tt.prober, Sweeper: fakeSweeper{}}), "fix the lease")
			require.NoError(t, err)

			assert.Equal(t, cycle.StopConditionUnmet, out.Stop)
			assert.Equal(t, tt.phase, out.Phase)
			assert.False(t, out.Verdict.Holds)
			assert.Contains(t, out.Verdict.Reason, tt.reason)
			assert.Equal(t, cycle.DefaultMaxAttempts, driver.calls[tt.phase],
				"a refused phase should use its whole attempt bound")
		})
	}
}

// What crosses a phase boundary is the standing goal, the change set, and
// the ledger. The prior phase's transcript is not carried, which is the
// difference between a cycle and a long conversation.
func TestRunner_CarriesTheLedgerAndChangeSetAndNotTheTranscript(t *testing.T) {
	t.Parallel()

	driver := newDriver(map[string][]phaseScript{
		"reproduce": {{
			changes: changed("internal/lease/lease_test.go"),
			hypotheses: []cycle.Hypothesis{
				{
					Cause: "the TTL is never read", Experiment: "printed the parsed value",
					Observation: "0s on every call", Verdict: "confirmed",
				},
				{
					Cause: "the clock is mocked wrong", Experiment: "swapped in the real clock",
					Observation: "no change", Verdict: "falsified",
				},
			},
		}},
		"fix":        {{changes: changed("internal/lease/lease.go"), complete: true}},
		"generalize": {{sweeps: []cycle.Sweep{{Pattern: "time.Duration($X)"}}, complete: true}},
	})

	prober := &fakeProber{rounds: [][]cycle.Observation{failing(), passing()}}
	runner := cycle.NewRunner(t.TempDir(), driver, &recordLog{})

	_, err := runner.Run(t.Context(), cycle.Fix(cycle.Checks{Prober: prober, Sweeper: fakeSweeper{}}),
		"make the lease TTL configurable")
	require.NoError(t, err)
	require.Len(t, driver.prompts, 3)

	fixPrompt := driver.prompts[1]
	assert.Contains(t, fixPrompt, "make the lease TTL configurable")
	assert.Contains(t, fixPrompt, "internal/lease/lease_test.go")
	assert.Contains(t, fixPrompt, "the clock is mocked wrong")
	assert.Contains(t, fixPrompt, "artifact-passes-gates-green")
	assert.NotContains(t, fixPrompt, "## Phase: reproduce")
}

// A cycle's phase transitions and Condition verdicts are events on the
// thread, so a client renders why a phase advanced rather than only that it
// did.
func TestRunner_LogsPhasesAndVerdicts(t *testing.T) {
	t.Parallel()

	driver := newDriver(map[string][]phaseScript{
		"reproduce": {{changes: changed("internal/lease/lease_test.go")}},
	})
	log := &recordLog{}
	runner := cycle.NewRunner(t.TempDir(), driver, log)

	_, err := runner.Run(t.Context(),
		cycle.Fix(cycle.Checks{Prober: &fakeProber{}, Sweeper: fakeSweeper{}}), "fix the lease")
	require.NoError(t, err)

	kinds := map[string]int{}

	for _, ev := range log.events {
		if ev.Kind != event.KindCycle {
			continue
		}

		kind, ok := ev.Detail["event"].(string)
		require.True(t, ok)
		kinds[kind]++

		assert.Equal(t, "fix", ev.Detail["cycle"])
	}

	assert.Equal(t, 1, kinds["phase_start"])
	assert.Equal(t, 1, kinds["phase_end"])
	assert.Equal(t, cycle.DefaultMaxAttempts, kinds["verdict"])
}

// The generalize phase's second exit: a sweep that does not discriminate is
// answered by a durable file this run wrote, and by nothing less.
func TestSweepAccounted_DurableArtifact(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "rules"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(root, "rules", "ttl.yml"), []byte("id: ttl\n"), 0o600))

	sweeper := fakeSweeper{hits: []cycle.Hit{{File: "internal/a.go", Line: 4}, {File: "internal/b.go", Line: 9}}}

	tests := []struct {
		name     string
		artifact string
		reason   string
		changes  []tool.Change
		dismiss  []cycle.Dismissal
		holds    bool
	}{
		{
			name: "every hit dismissed with a reason",
			dismiss: []cycle.Dismissal{
				{File: "internal/a.go", Line: 4, Reason: "checked elsewhere"},
				{File: "internal/b.go", Reason: "generated"},
			},
			holds:  true,
			reason: "dismissed with a reason",
		},
		{
			name:   "an untriaged hit and no artifact",
			holds:  false,
			reason: "internal/a.go:4",
		},
		{
			name:     "a named artifact this run wrote",
			artifact: "rules/ttl.yml",
			changes:  changed("rules/ttl.yml"),
			holds:    true,
			reason:   "does not discriminate",
		},
		{
			name:     "a named artifact nobody wrote",
			artifact: "rules/ttl.yml",
			holds:    false,
			reason:   "not in this cycle's change set",
		},
		{
			name:     "a named artifact that does not exist",
			artifact: "rules/missing.yml",
			changes:  changed("rules/missing.yml"),
			holds:    false,
			reason:   "does not exist",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			state := cycle.State{
				RepoRoot: root,
				Changes:  tt.changes,
				Ledger: cycle.Rows{Sweeps: []cycle.Sweep{{
					Pattern: "time.Duration($X)", Artifact: tt.artifact, Dismissed: tt.dismiss,
				}}},
			}

			verdict, err := cycle.SweepAccounted(sweeper).Holds(t.Context(), state)
			require.NoError(t, err)
			assert.Equal(t, tt.holds, verdict.Holds)
			assert.Contains(t, verdict.Reason, tt.reason)
		})
	}
}

// A project's own cycle replaces a built-in of the same name, and an exit
// condition the harness cannot evaluate is refused at load rather than
// advancing a phase on a claim.
func TestResolve(t *testing.T) {
	t.Parallel()

	checks := cycle.Checks{Prober: &fakeProber{}, Sweeper: fakeSweeper{}}

	built, err := cycle.Resolve([]cycle.Spec{{
		Name: "migrate",
		Phases: []cycle.PhaseSpec{
			{Name: "inventory", Goal: "list every call site", Exit: cycle.CondArtifactFails},
		},
	}}, checks)
	require.NoError(t, err)
	assert.Len(t, built, 2)
	assert.Len(t, built[cycle.FixCycle].Phases, 3)
	assert.Equal(t, "inventory", built["migrate"].Phases[0].Name)

	_, err = cycle.Resolve([]cycle.Spec{{
		Name:   "migrate",
		Phases: []cycle.PhaseSpec{{Name: "inventory", Exit: "the model says so"}},
	}}, checks)
	require.ErrorIs(t, err, cycle.ErrUnknownCondition)
}

// A sweep recorded twice for one pattern accumulates its dismissals rather
// than replacing them, since a run triages across several calls.
func TestLedger_MergesSweepsByPattern(t *testing.T) {
	t.Parallel()

	ledger := cycle.NewLedger()
	ledger.SetPhase("generalize")

	require.NoError(t, ledger.RecordSweep(cycle.Sweep{
		Pattern: "p", Dismissed: []cycle.Dismissal{{File: "a.go", Line: 1, Reason: "fine"}},
	}))
	require.NoError(t, ledger.RecordSweep(cycle.Sweep{
		Pattern: "p", Artifact: "rules/x.yml",
		Dismissed: []cycle.Dismissal{{File: "b.go", Line: 2, Reason: "generated"}},
	}))

	rows := ledger.Rows()
	require.Len(t, rows.Sweeps, 1)

	last, ok := rows.LastSweep()
	require.True(t, ok)
	assert.Equal(t, "rules/x.yml", last.Artifact)
	assert.Len(t, last.Dismissed, 2)
	assert.Equal(t, "generalize", last.Phase)
	assert.Contains(t, rows.Render(), "dismissed a.go:1")
}

// A repeated cause with the same verdict is refused, since a model that
// records the same row over and over is not doing the experiment.
func TestLedger_RefusesADuplicateHypothesis(t *testing.T) {
	t.Parallel()

	ledger := cycle.NewLedger()
	row := cycle.Hypothesis{Cause: "TTL never read", Experiment: "printed it", Observation: "0s", Verdict: "confirmed"}

	require.NoError(t, ledger.RecordHypothesis(row))
	require.ErrorIs(t, ledger.RecordHypothesis(row), cycle.ErrDuplicateHypothesis)

	row.Verdict = "falsified"
	require.NoError(t, ledger.RecordHypothesis(row))
	assert.Len(t, ledger.Rows().Hypotheses, 2)
}
