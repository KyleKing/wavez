package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
)

// ErrProcessNotStarted is returned by Process methods that need a running
// process when Start was never called or failed.
var ErrProcessNotStarted = errors.New("runtime: process not started")

// Process is the subset of os/exec's *Cmd that Server needs, so a test can
// drive a fake process instead of starting a real llama-server binary.
type Process interface {
	Start() error
	Wait() error
	Signal(sig os.Signal) error
	Kill() error
}

// ProcessFactory builds one unstarted Process for the given command line.
type ProcessFactory func(name string, args []string) Process

// execProcess is Process backed by os/exec.
type execProcess struct {
	cmd *exec.Cmd
}

// newExecProcess is the default ProcessFactory. It uses context.Background
// rather than a caller ctx because Server manages this process's lifetime
// itself, through Signal and Kill, never through context cancellation.
//
//nolint:ireturn // ProcessFactory's contract is the Process interface, not a concrete type
func newExecProcess(name string, args []string) Process {
	//nolint:gosec // name/args are this package's own resolved binary and flags, built from Config
	return &execProcess{cmd: exec.CommandContext(context.Background(), name, args...)}
}

func (p *execProcess) Start() error {
	if err := p.cmd.Start(); err != nil {
		return fmt.Errorf("starting %s: %w", p.cmd.Path, err)
	}

	return nil
}

func (p *execProcess) Wait() error {
	if err := p.cmd.Wait(); err != nil {
		return fmt.Errorf("waiting for %s: %w", p.cmd.Path, err)
	}

	return nil
}

func (p *execProcess) Signal(sig os.Signal) error {
	if p.cmd.Process == nil {
		return ErrProcessNotStarted
	}

	if err := p.cmd.Process.Signal(sig); err != nil {
		return fmt.Errorf("signaling %s: %w", p.cmd.Path, err)
	}

	return nil
}

func (p *execProcess) Kill() error {
	if p.cmd.Process == nil {
		return ErrProcessNotStarted
	}

	if err := p.cmd.Process.Kill(); err != nil {
		return fmt.Errorf("killing %s: %w", p.cmd.Path, err)
	}

	return nil
}

// ServedSlots is how many requests the local server processes at once, the
// `-np` it is started with. It is one because a 16 GB-class model on this
// laptop fits once, and it is exported because a second turn admitted
// against a single slot does not run concurrently, it queues inside
// llama-server where no scheduler can see it.
const ServedSlots = 1

// buildArgs is the exact llama-server command line Server starts, per
// DESIGN.md's Model routing section: n-gram speculation, tunable prefix
// reuse, explicit jinja templating, and an explicit served context.
func buildArgs(cfg Config) []string {
	args := []string{
		"-m", cfg.GGUFPath,
		"--host", "127.0.0.1",
		"--port", strconv.Itoa(cfg.Port),
		"-c", strconv.Itoa(cfg.contextSize()),
		"-np", strconv.Itoa(ServedSlots),
		"--spec-type", cfg.specType(),
		"--cache-reuse", strconv.Itoa(cfg.cacheReuse()),
		"--cache-ram", strconv.Itoa(cfg.cacheRAMMiB()),
		"--jinja",
		"--chat-template-kwargs", thinkingOff,
	}

	if cfg.Threads > 0 {
		args = append(args, "-t", strconv.Itoa(cfg.Threads))
	}
	if cfg.BatchSize > 0 {
		args = append(args, "-b", strconv.Itoa(cfg.BatchSize))
	}

	return args
}
