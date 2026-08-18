package app

import (
	"context"
	"fmt"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/cycle"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/router"
	"github.com/kyleking/wavez/internal/thread"
	"github.com/kyleking/wavez/internal/tool"
	"github.com/kyleking/wavez/internal/tools"
)

// Cycle returns the named cycle this project can run.
func (a *App) Cycle(name string) (cycle.Cycle, error) {
	c, ok := a.Cycles[name]
	if !ok {
		return cycle.Cycle{}, fmt.Errorf("%w: %q", cycle.ErrUnknownCycle, name)
	}

	return c, nil
}

// CycleDriver runs one cycle's phases for one thread. Every phase runs in
// its own thread, which is what keeps the prior phase's transcript out of
// the next phase's prefix: what crosses a boundary is the standing goal, the
// change set, and the ledger, all of which the Runner puts in the prompt.
type CycleDriver struct {
	app  *App
	base thread.ID
	dirs []string
	hint router.Input
}

// CycleDriver builds the Driver a cycle.Runner drives this project with.
// Base names the thread the cycle belongs to; each phase attempt opens a
// thread under it.
//
//nolint:ireturn // the daemon consumes this as cycle.Driver
func (a *App) CycleDriver(base thread.ID, dirs []string, hint router.Input) cycle.Driver {
	return &CycleDriver{app: a, base: base, dirs: dirs, hint: hint}
}

// Drive runs one phase attempt and reports how its Loop ended.
func (d *CycleDriver) Drive(ctx context.Context, at cycle.Attempt) (cycle.PhaseResult, error) {
	id := thread.ID(fmt.Sprintf("%s.%s.%d", d.base, at.Phase.Name, at.Number))

	th, err := d.app.OpenThread(id, d.dirs, thread.WithParent(d.base))
	if err != nil {
		return cycle.PhaseResult{}, fmt.Errorf("opening phase thread: %w", err)
	}

	registry := d.app.phaseRegistry(at.Phase, at.Ledger)
	loop := agent.New(d.app.Local, d.app.Hosted, registry, d.app.Permission, d.app.phaseOptions(at.Phase)...)

	prefix := agent.Prefix{System: d.app.SystemPrefix, Tools: specsOf(registry)}

	outcome, err := loop.Run(ctx, th, prefix, at.Prompt, d.hint)
	if err != nil {
		return cycle.PhaseResult{}, fmt.Errorf("running phase %s: %w", at.Phase.Name, err)
	}

	events, err := th.Log().Since(0)
	if err != nil {
		return cycle.PhaseResult{}, fmt.Errorf("reading phase %s: %w", at.Phase.Name, err)
	}

	return cycle.PhaseResult{
		Stop:      outcome.Condition(),
		Complete:  outcome.Stop == agent.StopComplete,
		Changes:   thread.ChangeSet(events),
		Turns:     outcome.Turns,
		ToolCalls: outcome.ToolCalls,
		SpendUSD:  outcome.HostedSpendUSD,
	}, nil
}

// phaseRegistry narrows the project's tools to the phase's set and adds the
// two a cycle brings: the ledger row recorder and the sweep. Both are bound
// to this run's ledger, so they exist per cycle rather than per project.
func (a *App) phaseRegistry(p cycle.Phase, ledger *cycle.Ledger) *tool.Registry {
	narrowed := a.Tools.Only(p.Tools...)

	built := make([]tool.Tool, 0, len(p.Tools)+2)

	for _, name := range narrowed.Names() {
		t, err := narrowed.Get(name)
		if err != nil {
			continue
		}

		built = append(built, t)
	}

	for _, name := range p.Tools {
		switch name {
		case "hypothesis":
			built = append(built, tools.NewHypothesis(ledger))
		case "sweep":
			built = append(built, tools.NewSweep(a.Root, a.sweeper, ledger))
		}
	}

	return tool.NewRegistry(built...)
}

// phaseOptions gates a phase's loop on the project's verifier only when the
// phase asked for it. An ungated phase drops the diff reviewer too: its
// change set is deliberately incomplete, so a judgment of whether the diff
// does what was asked is being asked too early.
func (a *App) phaseOptions(p cycle.Phase) []agent.Option {
	out := append([]agent.Option{}, a.loopBase...)
	if p.Gated {
		out = append(out, agent.WithVerifier(a.verifier), agent.WithReviewer(a.reviewer))
	}

	return out
}

func specsOf(registry *tool.Registry) []llm.ToolSpec {
	specs := registry.Specs()

	out := make([]llm.ToolSpec, 0, len(specs))
	for _, s := range specs {
		out = append(out, llm.ToolSpec{Name: s.Name, Description: s.Description, Schema: s.Schema})
	}

	return out
}
