package runtime_test

import (
	"time"

	"github.com/kyleking/wavez/internal/runtime"
)

// fakeTicker fires only when a test sends on its channel, so waitReady's
// retry loop advances on command instead of wall time passing.
type fakeTicker struct {
	ch chan time.Time
}

func fakeTickerFactory(ch chan time.Time) runtime.NewTickerFunc {
	return func(time.Duration) runtime.Ticker {
		return &fakeTicker{ch: ch}
	}
}

func (t *fakeTicker) C() <-chan time.Time { return t.ch }

func (*fakeTicker) Stop() {}
