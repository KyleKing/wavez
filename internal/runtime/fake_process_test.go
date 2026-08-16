package runtime_test

import (
	"os"
	"sync"
)

// fakeProcess is runtime.Process driven entirely by test code: Wait blocks
// until release is closed, and Signal/Kill record what they were sent so a
// test can assert the stop sequence without starting a real process.
type fakeProcess struct {
	startErr     error
	waitErr      error
	killErr      error
	signalErr    error
	release      chan struct{}
	signals      []os.Signal
	mu           sync.Mutex
	killed       bool
	stopOnSignal bool
}

func newFakeProcess() *fakeProcess {
	return &fakeProcess{release: make(chan struct{})}
}

func (p *fakeProcess) Start() error { return p.startErr }

func (p *fakeProcess) Wait() error {
	<-p.release

	return p.waitErr
}

func (p *fakeProcess) Signal(sig os.Signal) error {
	p.mu.Lock()
	p.signals = append(p.signals, sig)
	autoStop := p.stopOnSignal
	p.mu.Unlock()

	if autoStop {
		p.stop()
	}

	return p.signalErr
}

func (p *fakeProcess) Kill() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.killed = true

	if !p.released() {
		close(p.release)
	}

	return p.killErr
}

// stop unblocks Wait as if the process exited on its own, e.g. after
// receiving the signal a test recorded via Signal.
func (p *fakeProcess) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if !p.released() {
		close(p.release)
	}
}

func (p *fakeProcess) released() bool {
	select {
	case <-p.release:
		return true
	default:
		return false
	}
}

func (p *fakeProcess) signalCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return len(p.signals)
}

func (p *fakeProcess) wasKilled() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.killed
}
