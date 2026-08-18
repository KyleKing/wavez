package routine

import (
	"context"
	"fmt"

	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/tool"
)

// HistoryDepth is how many runs of one routine the panel and the API carry.
// It is what a duration sparkline needs and no more, so listing routines
// stays a cheap call.
const HistoryDepth = 20

// Info is one routine as a client sees it: what fires it, what it does, and
// how its recent runs went.
type Info struct {
	Name     string
	Triggers []Trigger
	Steps    []string
	Runs     []RunRecord
	Enabled  bool
}

// Service is the routine surface a client drives: list a project's
// routines with their recent runs, and run one by name. The set is read
// through a func so a config reload swaps it without rebuilding the
// service.
type Service struct {
	runner  *Runner
	history *History
	set     func() *Set
	root    string
}

// NewService builds the Service over the compiled set set returns.
func NewService(root string, runner *Runner, history *History, set func() *Set) *Service {
	return &Service{root: root, runner: runner, history: history, set: set}
}

// List returns every routine the project has, built-ins included, sorted by
// name.
func (s *Service) List() ([]Info, error) {
	runs, err := s.history.Runs()
	if err != nil {
		return nil, err
	}

	byRoutine := make(map[string][]RunRecord, len(runs))
	for _, r := range runs {
		byRoutine[r.Routine] = append(byRoutine[r.Routine], r)
	}

	all := s.set().All()
	out := make([]Info, 0, len(all))

	for _, rt := range all {
		recent := byRoutine[rt.Name]
		if len(recent) > HistoryDepth {
			recent = recent[len(recent)-HistoryDepth:]
		}

		out = append(out, Info{
			Name:     rt.Name,
			Triggers: rt.Triggers,
			Steps:    stepNames(rt),
			Enabled:  rt.Enabled,
			Runs:     recent,
		})
	}

	return out, nil
}

// Run executes the routine named name against the project's working copy.
// A manual run carries no change set, so its steps see the whole project
// rather than a batch.
func (s *Service) Run(ctx context.Context, name string) (RunRecord, error) {
	rt, ok := s.set().Get(name)
	if !ok {
		return RunRecord{}, fmt.Errorf("%w %q", ErrUnknownRoutine, name)
	}

	rec, err := s.runner.Run(ctx, rt, TriggerManual, Env{
		Root:      s.root,
		Selection: gate.Selection{Level: gate.LevelPackage},
	})
	if err != nil {
		return rec, err
	}

	return rec, nil
}

func stepNames(rt *Routine) []string {
	out := make([]string, 0, len(rt.Steps))
	for _, s := range rt.Steps {
		out = append(out, s.Name)
	}

	return out
}

// ChangeRunFunc wraps next so a debounced batch runs the change-triggered
// routines after the gates it already ran. Routines call into gates rather
// than the other way round, so this is the only place the two meet, and a
// routine that fails does not change the gate result the model sees.
func ChangeRunFunc(root string, next gate.RunFunc, runner *Runner, set func() *Set) gate.RunFunc {
	return func(ctx context.Context, changes []tool.Change) gate.RunResult {
		result := next(ctx, changes)

		// gate.RunResult carries each gate's level but not the Selection they
		// ran against, so a change-triggered routine gets the batch's changes
		// and the widest level rather than a narrowed test set.
		env := Env{Root: root, Changes: changes, Selection: gate.Selection{Level: gate.LevelPackage}}
		for _, rt := range set().Triggered(TriggerChange) {
			if !matchesAny(rt, changes) {
				continue
			}

			//nolint:errcheck // a routine's outcome is its history entry; the gate result the model sees is unaffected
			_, _ = runner.Run(ctx, rt, TriggerChange, env)
		}

		return result
	}
}

func matchesAny(rt *Routine, changes []tool.Change) bool {
	if len(rt.Paths) == 0 {
		return true
	}

	for _, c := range changes {
		if rt.MatchesPath(c.Path) {
			return true
		}
	}

	return false
}
