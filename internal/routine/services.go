package routine

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/kyleking/wavez/internal/gate"
)

// Service action names. They are a pair rather than one action with a
// direction, because a step naming what it does is what makes a routine
// readable, and because an absent direction must never mean "stop".
const (
	ServiceUpName   = "service.up"
	ServiceDownName = "service.down"
)

// DefaultServiceTimeout bounds one `up` or `down`. A compose target pulling
// images is the reason it is generous.
const DefaultServiceTimeout = 5 * time.Minute

// DefaultReadyInterval is how often readiness is retried while waiting.
const DefaultReadyInterval = 500 * time.Millisecond

// ErrUnknownService reports a step naming a service the project did not
// define.
var ErrUnknownService = errors.New("no such service")

// ServiceDef is one long-running thing a project can bring up and take down:
// a compose target, a database, a fake API. Up and Down are argv, executed
// directly and never through a shell, for the reason `run` is.
//
// Dir from the argv they belong with for eight bytes
//
//nolint:govet // fieldalignment wants the strings last, which splits Name and
type ServiceDef struct {
	Up   []string
	Down []string
	// Ready is a command that exits zero once the service can be used.
	// Without one, `up` returning is all the readiness there is, which is
	// enough for something that blocks until it is listening and not enough
	// for anything that forks.
	Ready      []string
	Name       string
	Dir        string
	Timeout    time.Duration
	ReadyWait  time.Duration
	ReadyEvery time.Duration
}

// Services starts and stops what a project declared, counting who asked.
//
// The count is the whole point. A service exists because it is expensive:
// the reason to declare a compose target rather than leave it running is
// that it should be up only while something needs it. Two routines wanting
// the same database must not start it twice, and the first of them to finish
// must not take it away from the second.
type Services struct {
	defs map[string]ServiceDef
	held map[string]int
	mu   sync.Mutex
}

// NewServices builds a Services over defs, keyed by name.
func NewServices(defs []ServiceDef) *Services {
	byName := make(map[string]ServiceDef, len(defs))
	for i := range defs {
		byName[defs[i].Name] = defs[i]
	}

	return &Services{defs: byName, held: map[string]int{}}
}

// Up starts the service if nothing else is holding it, and records the hold
// either way. A service already up is not started again.
func (s *Services) Up(ctx context.Context, name string) error {
	def, first, err := s.claim(name)
	if err != nil {
		return err
	}

	if !first {
		return nil
	}

	if err := s.start(ctx, def); err != nil {
		s.release(name)

		return err
	}

	return nil
}

// Down releases one hold and stops the service once the last is gone. It is
// not an error to release a service nothing holds, because a routine whose
// `up` failed still runs its `down` and telling it off for tidying up would
// turn one failure into two.
func (s *Services) Down(ctx context.Context, name string) error {
	def, last, err := s.releaseHeld(name)
	if err != nil {
		return err
	}

	if !last || len(def.Down) == 0 {
		return nil
	}

	if _, err := runArgv(ctx, def.Down, def.Dir, def.timeout()); err != nil {
		return fmt.Errorf("stopping %s: %w", name, err)
	}

	return nil
}

// claim records a hold and reports whether it is the first.
func (s *Services) claim(name string) (ServiceDef, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	def, ok := s.defs[name]
	if !ok {
		return ServiceDef{}, false, fmt.Errorf("%w: %s", ErrUnknownService, name)
	}

	s.held[name]++

	return def, s.held[name] == 1, nil
}

func (s *Services) release(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.held[name] > 0 {
		s.held[name]--
	}
}

// releaseHeld drops one hold and reports whether it was the last one.
func (s *Services) releaseHeld(name string) (ServiceDef, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	def, ok := s.defs[name]
	if !ok {
		return ServiceDef{}, false, fmt.Errorf("%w: %s", ErrUnknownService, name)
	}

	if s.held[name] == 0 {
		return def, false, nil
	}

	s.held[name]--

	return def, s.held[name] == 0, nil
}

// start brings the service up and waits for it to be ready.
func (*Services) start(ctx context.Context, def ServiceDef) error {
	if len(def.Up) > 0 {
		if _, err := runArgv(ctx, def.Up, def.Dir, def.timeout()); err != nil {
			return fmt.Errorf("starting %s: %w", def.Name, err)
		}
	}

	return waitReady(ctx, def)
}

// waitReady retries the readiness command until it passes or the wait runs
// out. A service that never becomes ready fails the step that wanted it,
// rather than leaving the next step to fail for a reason it cannot explain.
func waitReady(ctx context.Context, def ServiceDef) error {
	if len(def.Ready) == 0 {
		return nil
	}

	deadline := time.Now().Add(def.readyWait())
	every := def.readyEvery()

	for {
		out, err := runArgv(ctx, def.Ready, def.Dir, def.timeout())
		if err == nil {
			return nil
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not become ready in %s: %w: %s",
				def.Name, def.readyWait(), err, out)
		}

		timer := time.NewTimer(every)
		select {
		case <-ctx.Done():
			timer.Stop()

			return fmt.Errorf("waiting for %s: %w", def.Name, ctx.Err())
		case <-timer.C:
		}
	}
}

func (d ServiceDef) timeout() time.Duration {
	if d.Timeout > 0 {
		return d.Timeout
	}

	return DefaultServiceTimeout
}

func (d ServiceDef) readyWait() time.Duration {
	if d.ReadyWait > 0 {
		return d.ReadyWait
	}

	return DefaultServiceTimeout
}

func (d ServiceDef) readyEvery() time.Duration {
	if d.ReadyEvery > 0 {
		return d.ReadyEvery
	}

	return DefaultReadyInterval
}

// runArgv executes one argv and returns its combined output.
func runArgv(ctx context.Context, argv []string, dir string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	//nolint:gosec // argv comes from the project's own config and never through a shell
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("running %s: %w", argv[0], err)
	}

	return string(out), nil
}

// ServiceActions are the two actions a routine step invokes to hold a
// service for the work around it.
func ServiceActions(services *Services) []Action {
	return []Action{
		{Name: ServiceUpName, Bind: bindService(services, true)},
		{Name: ServiceDownName, Bind: bindService(services, false)},
	}
}

func bindService(services *Services, up bool) func(map[string]any) (Bound, error) {
	return func(params map[string]any) (Bound, error) {
		if err := rejectUnknown(params, "name"); err != nil {
			return Bound{}, err
		}

		name, err := optionalString(params, "name", "")
		if err != nil {
			return Bound{}, err
		}

		if name == "" {
			return Bound{}, fmt.Errorf("%w: name is required", ErrEmptyParam)
		}

		if _, known := services.defs[name]; !known {
			return Bound{}, fmt.Errorf("%w: %s", ErrUnknownService, name)
		}

		return Bound{
			// The service's own name, so two steps holding the same service
			// serialize and two holding different ones do not.
			Resources: []string{"service:" + name},
			Run: func(ctx context.Context, _ Env) (Outcome, error) {
				return serviceOutcome(ctx, services, name, up)
			},
		}, nil
	}
}

// serviceOutcome reports the hold as one examined unit, so a step that held
// a service is not read as one that abstained.
func serviceOutcome(ctx context.Context, services *Services, name string, up bool) (Outcome, error) {
	var (
		action string
		err    error
	)

	if up {
		action, err = ServiceUpName, services.Up(ctx, name)
	} else {
		action, err = ServiceDownName, services.Down(ctx, name)
	}

	if err != nil {
		// A service that would not start is this step's failure rather than
		// the runner's error: an error stops the routine where a failure is
		// reported and the routine decides.
		return Outcome{ //nolint:nilerr // as above: the failure is the outcome
			Examined: 1,
			Failures: []gate.TrimmedFailure{{Test: action, Frames: []string{err.Error()}}},
		}, nil
	}

	return Outcome{Examined: 1, Pass: true}, nil
}
