// Package routine implements DESIGN.md's "Routines (M2)": named DAGs of
// deterministic actions, defined in ".wavez.pkl", with no model in them. A
// Definition is the config shape, Compile turns one into a validated DAG
// bound to real handlers, and Runner executes that DAG with the same
// resource serialization and output trimming the gates use. Routines call
// into internal/gate rather than the other way round.
package routine

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

// Trigger names what fires a routine.
type Trigger string

// Triggers a routine may declare, matching the Trigger typealias in
// config/pkl/Wavez.pkl.
const (
	TriggerChange       Trigger = "change"
	TriggerManual       Trigger = "manual"
	TriggerSchedule     Trigger = "schedule"
	TriggerThreadStart  Trigger = "thread-start"
	TriggerThreadFinish Trigger = "thread-finish"
)

// Concurrency names what happens to a run arriving while its concurrency
// key is busy.
type Concurrency string

// Concurrency strategies, matching the Concurrency typealias in
// config/pkl/Wavez.pkl.
const (
	// Queue waits for the key to free up, oldest waiter first.
	Queue Concurrency = "queue"
	// CancelInProgress cancels the running instance and takes its place.
	CancelInProgress Concurrency = "cancel-in-progress"
	// RoundRobin admits waiting routines in rotation, so one routine firing
	// repeatedly cannot starve another sharing the key.
	RoundRobin Concurrency = "round-robin"
)

// StepDef is one node of a routine's DAG as the config file declares it.
type StepDef struct {
	Params  map[string]any
	Name    string
	Action  string
	Parents []string
}

// Definition is one routine as the config file declares it. Compile turns
// it into a Routine.
type Definition struct {
	Name           string
	ConcurrencyKey string
	Concurrency    Concurrency
	Triggers       []Trigger
	Paths          []string
	Steps          []StepDef
	Interval       time.Duration
	Enabled        bool
}

// Errors Compile returns for a routine the runner could never execute.
var (
	ErrNoSteps        = errors.New("routine has no steps")
	ErrDuplicateStep  = errors.New("duplicate step name")
	ErrUnknownParent  = errors.New("step names an unknown parent")
	ErrCyclicDAG      = errors.New("steps form a cycle")
	ErrUnknownAction  = errors.New("unknown action")
	ErrUnknownRoutine = errors.New("unknown routine")
	ErrDisabled       = errors.New("routine is disabled")
	ErrBadInterval    = errors.New("a schedule trigger needs intervalSeconds of at least 30")
)

// Step is one compiled node: its action is already bound to validated
// parameters, so running it cannot fail on configuration.
type Step struct {
	bound   Bound
	Name    string
	Action  string
	Parents []string
}

// Routine is one compiled DAG, ready to run.
type Routine struct {
	Name           string
	ConcurrencyKey string
	Concurrency    Concurrency
	Triggers       []Trigger
	Paths          []string
	Steps          []Step
	// Order holds step indexes in dependency order, grouped into waves that
	// may run concurrently.
	Order    [][]int
	Interval time.Duration
	Enabled  bool
}

// Triggered reports whether t fires this routine.
func (r *Routine) Triggered(t Trigger) bool {
	if !r.Enabled {
		return false
	}

	for _, have := range r.Triggers {
		if have == t {
			return true
		}
	}

	return false
}

// MatchesPath reports whether a change to path can fire this routine. A
// routine with no path patterns matches every change.
func (r *Routine) MatchesPath(path string) bool {
	if len(r.Paths) == 0 {
		return true
	}

	for _, pattern := range r.Paths {
		if ok, err := filepath.Match(pattern, path); err == nil && ok {
			return true
		}

		if ok, err := filepath.Match(pattern, filepath.Base(path)); err == nil && ok {
			return true
		}
	}

	return false
}

// key is the concurrency key runs of this routine serialize on.
func (r *Routine) key() string {
	if r.ConcurrencyKey != "" {
		return r.ConcurrencyKey
	}

	return r.Name
}

// Compile validates def against reg and binds every step's parameters,
// so a routine that would fail on a bad action or a cyclic DAG fails at
// config load rather than mid-run.
func Compile(def Definition, reg *Registry) (*Routine, error) {
	if def.Enabled && len(def.Steps) == 0 {
		return nil, fmt.Errorf("routine %q: %w", def.Name, ErrNoSteps)
	}

	if def.Enabled && slices.Contains(def.Triggers, TriggerSchedule) && def.Interval < MinInterval {
		return nil, fmt.Errorf("routine %q: %w, got %s", def.Name, ErrBadInterval, def.Interval)
	}

	steps, err := bindSteps(def, reg)
	if err != nil {
		return nil, err
	}

	order, err := topoWaves(steps)
	if err != nil {
		return nil, fmt.Errorf("routine %q: %w", def.Name, err)
	}

	concurrency := def.Concurrency
	if concurrency == "" {
		concurrency = Queue
	}

	return &Routine{
		Name:           def.Name,
		Triggers:       append([]Trigger(nil), def.Triggers...),
		Paths:          append([]string(nil), def.Paths...),
		Steps:          steps,
		Order:          order,
		ConcurrencyKey: def.ConcurrencyKey,
		Concurrency:    concurrency,
		Interval:       def.Interval,
		Enabled:        def.Enabled,
	}, nil
}

func bindSteps(def Definition, reg *Registry) ([]Step, error) {
	seen := make(map[string]struct{}, len(def.Steps))
	steps := make([]Step, 0, len(def.Steps))

	for _, sd := range def.Steps {
		if _, dup := seen[sd.Name]; dup {
			return nil, fmt.Errorf("routine %q step %q: %w", def.Name, sd.Name, ErrDuplicateStep)
		}

		seen[sd.Name] = struct{}{}

		action, ok := reg.Lookup(sd.Action)
		if !ok {
			return nil, fmt.Errorf("routine %q step %q: %w %q", def.Name, sd.Name, ErrUnknownAction, sd.Action)
		}

		bound, err := action.Bind(sd.Params)
		if err != nil {
			return nil, fmt.Errorf("routine %q step %q action %q: %w", def.Name, sd.Name, sd.Action, err)
		}

		steps = append(steps, Step{
			Name:    sd.Name,
			Action:  sd.Action,
			Parents: append([]string(nil), sd.Parents...),
			bound:   bound,
		})
	}

	for _, s := range steps {
		for _, p := range s.Parents {
			if _, ok := seen[p]; !ok {
				return nil, fmt.Errorf("routine %q step %q: %w %q", def.Name, s.Name, ErrUnknownParent, p)
			}
		}
	}

	return steps, nil
}

// topoWaves groups step indexes into dependency waves: every step in wave n
// has all its parents in waves before n, so a wave's steps may run
// concurrently.
func topoWaves(steps []Step) ([][]int, error) {
	index := make(map[string]int, len(steps))
	for i, s := range steps {
		index[s.Name] = i
	}

	done := make([]bool, len(steps))

	var waves [][]int

	for placed := 0; placed < len(steps); {
		var wave []int

		for i, s := range steps {
			if done[i] || !parentsDone(s, index, done) {
				continue
			}

			wave = append(wave, i)
		}

		if len(wave) == 0 {
			return nil, ErrCyclicDAG
		}

		for _, i := range wave {
			done[i] = true
		}

		placed += len(wave)
		waves = append(waves, wave)
	}

	return waves, nil
}

func parentsDone(s Step, index map[string]int, done []bool) bool {
	for _, p := range s.Parents {
		if !done[index[p]] {
			return false
		}
	}

	return true
}

// Set is a project's compiled routines, keyed by name.
type Set struct {
	byName map[string]*Routine
	// Hash is the config content hash the set was compiled from. Cache uses
	// it to decide whether a compiled set is still the file's own.
	Hash string
}

// CompileSet compiles every definition, merged over the built-ins: a
// definition replaces the built-in of the same name outright. It fails on
// the first routine that will not compile, since a project whose config
// names a missing action is misconfigured whether or not that routine ever
// fires.
func CompileSet(defs map[string]Definition, reg *Registry, hash string) (*Set, error) {
	merged := make(map[string]Definition, len(defs)+len(builtinDefinitions))

	for _, name := range sortedKeys(builtinDefinitions) {
		// A built-in exists only because its gate does: a caller that never
		// registered that gate's action gets no routine for it rather than a
		// config error it cannot fix.
		if registered(builtinDefinitions[name], reg) {
			merged[name] = builtinDefinitions[name]
		}
	}

	for _, name := range sortedKeys(defs) {
		def := defs[name]
		def.Name = name
		merged[name] = def
	}

	set := &Set{byName: make(map[string]*Routine, len(merged)), Hash: hash}

	for _, name := range sortedKeys(merged) {
		def := merged[name]
		def.Name = name

		if !def.Enabled && len(def.Steps) == 0 {
			set.byName[name] = &Routine{Name: name, Enabled: false, Concurrency: Queue}

			continue
		}

		compiled, err := Compile(def, reg)
		if err != nil {
			return nil, err
		}

		set.byName[name] = compiled
	}

	return set, nil
}

// Get returns the compiled routine named name.
func (s *Set) Get(name string) (*Routine, bool) {
	if s == nil {
		return nil, false
	}

	r, ok := s.byName[name]

	return r, ok
}

// All returns every routine, disabled ones included, sorted by name.
func (s *Set) All() []*Routine {
	if s == nil {
		return nil
	}

	out := make([]*Routine, 0, len(s.byName))
	for _, name := range sortedKeys(s.byName) {
		out = append(out, s.byName[name])
	}

	return out
}

// Triggered returns every enabled routine t fires, sorted by name.
func (s *Set) Triggered(t Trigger) []*Routine {
	var out []*Routine

	for _, r := range s.All() {
		if r.Triggered(t) {
			out = append(out, r)
		}
	}

	return out
}

// DisabledGates names the gates a project turned off by disabling their
// built-in routine, so the change-gate pipeline can drop them. Disabling a
// built-in is the only way DESIGN.md's "gates ship as built-in routines the
// user can override or disable" reaches the gates themselves.
func (s *Set) DisabledGates() []string {
	var out []string

	for _, r := range s.All() {
		gate, ok := strings.CutPrefix(r.Name, builtinPrefix)
		if !ok || r.Enabled {
			continue
		}

		out = append(out, gate)
	}

	return out
}

func registered(def Definition, reg *Registry) bool {
	for _, s := range def.Steps {
		if _, ok := reg.Lookup(s.Action); !ok {
			return false
		}
	}

	return true
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	sort.Strings(out)

	return out
}
