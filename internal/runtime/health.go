package runtime

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// DefaultPollInterval is how often Server retries the health endpoint
// between attempts while waiting for readiness.
const DefaultPollInterval = 200 * time.Millisecond

// HealthChecker reports whether baseURL's server is ready to serve
// requests, returning nil only when it is.
type HealthChecker func(ctx context.Context, baseURL string) error

// Ticker abstracts a repeating timer so WaitReady never sleeps in tests: a
// fake Ticker fires on command instead of wall time passing.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

// NewTickerFunc builds a Ticker that fires every d.
type NewTickerFunc func(d time.Duration) Ticker

// realTicker is Ticker backed by time.Ticker.
type realTicker struct {
	t *time.Ticker
}

//nolint:ireturn // NewTickerFunc's contract is the Ticker interface, not a concrete type
func newRealTicker(d time.Duration) Ticker {
	return &realTicker{t: time.NewTicker(d)}
}

func (r *realTicker) C() <-chan time.Time { return r.t.C }

func (r *realTicker) Stop() { r.t.Stop() }

// HTTPHealthCheck is the default HealthChecker: GET baseURL/health, ready
// only on a 200 response.
func HTTPHealthCheck(ctx context.Context, baseURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/health", http.NoBody)
	if err != nil {
		return fmt.Errorf("building health check request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("health check request: %w", err)
	}
	//nolint:errcheck // response body is discarded, close error carries no actionable information
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: status %d", ErrNotReady, resp.StatusCode)
	}

	return nil
}

// ErrNotReady is returned by a HealthChecker when the server responded but
// is not yet ready.
var ErrNotReady = errors.New("runtime: server not ready")

// waitReady polls check against healthURL until it succeeds or ctx is
// done, retrying every interval via newTicker rather than sleeping, so a
// test can drive the retry loop deterministically.
func waitReady(
	ctx context.Context,
	healthURL string,
	check HealthChecker,
	newTicker NewTickerFunc,
	interval time.Duration,
) error {
	if err := check(ctx, healthURL); err == nil {
		return nil
	}

	ticker := newTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for llama-server readiness: %w", ctx.Err())
		case <-ticker.C():
			if err := check(ctx, healthURL); err == nil {
				return nil
			}
		}
	}
}
