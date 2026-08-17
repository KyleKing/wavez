package runtime

import (
	"context"
	"fmt"
	"syscall"
	"time"
)

// startDeps carries everything Start needs to inject for tests: a fake
// process, a fake health check, and a fake ticker, so no test starts a
// real llama-server or sleeps real time.
type startDeps struct {
	newProcess   ProcessFactory
	check        HealthChecker
	newTicker    NewTickerFunc
	pollInterval time.Duration
}

// Server is one running llama-server instance.
type Server struct {
	process   Process
	waitErr   error
	done      chan struct{}
	baseURL   string
	healthURL string
}

// BaseURL is the OpenAI-compatible endpoint root an openaic.Client should
// use (openaic.WithBaseURL), so a caller never rebuilds the URL by hand.
func (s *Server) BaseURL() string { return s.baseURL }

// Stop signals llama-server to exit, waits until either it exits or ctx is
// done, and kills it if the deadline passes first. A leaked server holds
// 6 GB of RAM, so callers must always give ctx a deadline.
func (s *Server) Stop(ctx context.Context) error {
	select {
	case <-s.done:
		return s.waitErr
	default:
	}

	if err := s.process.Signal(syscall.SIGTERM); err != nil {
		return fmt.Errorf("signaling llama-server to stop: %w", err)
	}

	select {
	case <-s.done:
		return s.waitErr
	case <-ctx.Done():
		if err := s.process.Kill(); err != nil {
			return fmt.Errorf("killing llama-server after stop timeout: %w", err)
		}

		<-s.done

		return fmt.Errorf("llama-server did not exit before timeout, killed: %w", ctx.Err())
	}
}

// startServer starts one llama-server process from cfg and waits for it to
// report ready, killing it if readiness never arrives before ctx is done.
func startServer(ctx context.Context, cfg Config, deps startDeps) (*Server, error) {
	proc := deps.newProcess(cfg.binary(), buildArgs(cfg))
	if err := proc.Start(); err != nil {
		return nil, fmt.Errorf("starting llama-server: %w", err)
	}

	s := &Server{
		process:   proc,
		baseURL:   LocalBaseURL(cfg.Port),
		healthURL: localHealthURL(cfg.Port),
		done:      make(chan struct{}),
	}

	go func() {
		s.waitErr = proc.Wait()
		close(s.done)
	}()

	if err := waitReady(ctx, s.healthURL, deps.check, deps.newTicker, deps.pollInterval); err != nil {
		//nolint:errcheck // best-effort cleanup after a failed start; the readiness error is what the caller sees
		_ = proc.Kill()
		<-s.done

		return nil, fmt.Errorf("llama-server did not become ready: %w", err)
	}

	return s, nil
}
