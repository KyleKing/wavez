package app

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/config"
	"github.com/kyleking/wavez/internal/gate"
	"github.com/kyleking/wavez/internal/routine"
)

const routineHistoryFileName = "routines.log"

// scheduleTick is how often the timekeeper looks for a due routine, which
// bounds how late one can start. It sits well under routine.MinInterval so
// the shortest cadence a project may declare is still met on time.
const scheduleTick = 5 * time.Second

// gateBundle is what buildGates assembles: the change-gate runner, the
// coverage adapter selection reads, the verification round, and the routine
// layer wrapped around them.
type gateBundle struct {
	runner   *gate.Runner
	adapter  *gate.CoverageAdapter
	verifier *GateVerifier
	routines routineLayer
	// runScope names the run in progress, so the change gate that begins a
	// run and the gate pipeline that lints under it agree on one identity.
	runScope *gate.RunScope
}

// routineLayer is the project's compiled routines and the pieces that run
// them.
type routineLayer struct {
	set        *routine.Set
	runner     *routine.Runner
	service    *RoutineService
	timekeeper *routine.Timekeeper
	compiled   func() *routine.Set
}

// buildRoutines compiles the project's routines against a registry holding
// one action per gate plus `run`, so a routine can invoke the same checks
// the change path does without either side knowing about the other. A
// routine that will not compile fails App construction, which is the point
// of validating at load.
func buildRoutines(
	root, stateDir string, cfg config.Config, resources *gate.ResourceSet, gates []gate.Gate,
) (routineLayer, error) {
	actions := make([]routine.Action, 0, len(gates)+1)
	for _, g := range gates {
		actions = append(actions, routine.GateAction(g))
	}

	actions = append(actions, routine.RunAction(root), routine.SemgrepAction(root))
	actions = append(actions, routine.ServiceActions(routine.NewServices(serviceDefs(root, cfg)))...)

	registry := routine.NewRegistry(actions...)

	hash, err := routine.HashFile(filepath.Join(root, config.FileName))
	if err != nil {
		return routineLayer{}, fmt.Errorf("hashing the project config: %w", err)
	}

	var cache routine.Cache

	set, err := cache.Compiled(hash, cfg.Routines, registry)
	if err != nil {
		return routineLayer{}, fmt.Errorf("compiling routines: %w", err)
	}

	history, err := routine.OpenHistory(filepath.Join(stateDir, routineHistoryFileName))
	if err != nil {
		return routineLayer{}, fmt.Errorf("opening routine history: %w", err)
	}

	runner := routine.NewRunner(gate.RealClock{}, resources, history)
	compiled := func() *routine.Set { return set }

	svc := routine.NewService(root, runner, history, compiled)

	return routineLayer{
		set:        set,
		runner:     runner,
		compiled:   compiled,
		service:    &RoutineService{svc: svc},
		timekeeper: routine.NewTimekeeper(root, runner, compiled, scheduleTick),
	}, nil
}

// RoutineService is the routine layer in the shape a client speaks, so the
// daemon lists and runs routines without knowing how one is compiled.
type RoutineService struct {
	svc *routine.Service
}

// ThreadStarted runs the routines a thread's first turn fires.
func (s *RoutineService) ThreadStarted(ctx context.Context) {
	s.svc.Fire(ctx, routine.TriggerThreadStart)
}

// ThreadFinished runs the routines a finished turn fires. It runs on the
// turn's own goroutine, so what it checks is the tree the run left.
func (s *RoutineService) ThreadFinished(ctx context.Context) {
	s.svc.Fire(ctx, routine.TriggerThreadFinish)
}

// List returns every routine the project has, built-ins included.
func (s *RoutineService) List() ([]api.RoutineInfo, error) {
	infos, err := s.svc.List()
	if err != nil {
		return nil, fmt.Errorf("listing routines: %w", err)
	}

	out := make([]api.RoutineInfo, 0, len(infos))
	for _, info := range infos {
		out = append(out, routineInfo(info))
	}

	return out, nil
}

// Run runs the routine named name and returns its refreshed row.
func (s *RoutineService) Run(ctx context.Context, name string) (api.RoutineInfo, error) {
	if _, err := s.svc.Run(ctx, name); err != nil {
		return api.RoutineInfo{}, fmt.Errorf("running routine %s: %w", name, err)
	}

	infos, err := s.List()
	if err != nil {
		return api.RoutineInfo{}, err
	}

	for _, info := range infos {
		if info.Name == name {
			return info, nil
		}
	}

	return api.RoutineInfo{}, fmt.Errorf("%w %q", routine.ErrUnknownRoutine, name)
}

func routineInfo(info routine.Info) api.RoutineInfo {
	triggers := make([]string, 0, len(info.Triggers))
	for _, t := range info.Triggers {
		triggers = append(triggers, string(t))
	}

	runs := make([]api.RoutineRun, 0, len(info.Runs))
	for _, r := range info.Runs {
		runs = append(runs, api.RoutineRun{
			Started:   r.Timestamp,
			Trigger:   string(r.Trigger),
			Duration:  r.Duration,
			Pass:      r.Pass,
			Failed:    failedSteps(r),
			Abstained: stepsWithStatus(r, routine.StatusAbstained),
		})
	}

	return api.RoutineInfo{
		Name:     info.Name,
		Triggers: triggers,
		Steps:    info.Steps,
		Enabled:  info.Enabled,
		Runs:     runs,
	}
}

func failedSteps(rec routine.RunRecord) []string {
	var out []string

	for _, s := range rec.Steps {
		if s.Status != routine.StatusPass && s.Status != routine.StatusAbstained {
			out = append(out, s.Name+" "+string(s.Status))
		}
	}

	return out
}

func stepsWithStatus(rec routine.RunRecord, status routine.Status) []string {
	var out []string

	for _, s := range rec.Steps {
		if s.Status == status {
			out = append(out, s.Name)
		}
	}

	return out
}

// serviceDefs translates the project's declared services into the routine
// package's own shape, so routines stay independent of the config package.
// A relative dir is resolved against the project root here, the way `run`
// resolves its own.
func serviceDefs(root string, cfg config.Config) []routine.ServiceDef {
	out := make([]routine.ServiceDef, 0, len(cfg.Services))
	for _, s := range cfg.Services {
		dir := s.Dir
		if dir == "" {
			dir = "."
		}

		out = append(out, routine.ServiceDef{
			Name: s.Name, Up: s.Up, Down: s.Down, Ready: s.Ready,
			Dir:       filepath.Join(root, filepath.FromSlash(dir)),
			Timeout:   s.Timeout,
			ReadyWait: s.ReadyWait,
		})
	}

	return out
}
