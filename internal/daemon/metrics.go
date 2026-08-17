package daemon

import (
	"sync"
	"time"

	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/llm"
)

// usage totals one thread's token counts, read back off its own event log
// rather than kept alongside it: thread.AppendAssistant already records each
// turn's llm.Usage, so the daemon has the numbers without the agent loop
// reporting them a second time.
type usage struct {
	input     int
	output    int
	cacheRead int
	// context is the last turn's prompt plus completion, which is what
	// occupies the model's window now. The other counts are lifetime sums.
	context int
}

func (u *usage) add(v llm.Usage) {
	u.input += v.InputTokens
	u.output += v.OutputTokens
	u.cacheRead += v.CacheReadTokens
	u.context = v.InputTokens + v.OutputTokens
}

func (u usage) tokens() int { return u.input + u.output }

// usageFromEvent reads the per-turn counts thread.AppendAssistant puts on its
// KindAgent event. A live subscriber sees the *llm.Usage that was appended
// while a subscriber replaying the log off disk sees it decoded from JSON, so
// both shapes reach here.
func usageFromEvent(ev event.Event) (llm.Usage, bool) {
	raw, ok := ev.Detail["usage"]
	if !ok {
		return llm.Usage{}, false
	}

	switch v := raw.(type) {
	case *llm.Usage:
		if v == nil {
			return llm.Usage{}, false
		}

		return *v, true
	case llm.Usage:
		return v, true
	case map[string]any:
		return llm.Usage{
			InputTokens:     jsonInt(v, "input_tokens"),
			OutputTokens:    jsonInt(v, "output_tokens"),
			CacheReadTokens: jsonInt(v, "cache_read_tokens"),
		}, true
	default:
		return llm.Usage{}, false
	}
}

func jsonInt(m map[string]any, key string) int {
	f, ok := m[key].(float64)
	if !ok {
		return 0
	}

	return int(f)
}

// spendLedger totals hosted spend for the current day. It is in-memory, so a
// daemon restart starts the day's total over.
type spendLedger struct {
	now   func() time.Time
	day   time.Time
	total float64
	mu    sync.Mutex
}

func newSpendLedger(now func() time.Time) *spendLedger {
	if now == nil {
		now = time.Now
	}

	return &spendLedger{now: now}
}

func (s *spendLedger) add(usd float64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.rollLocked()
	s.total += usd
}

func (s *spendLedger) today() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.rollLocked()

	return s.total
}

func (s *spendLedger) rollLocked() {
	now := s.now()

	y, m, d := now.Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, now.Location())

	if !day.Equal(s.day) {
		s.day, s.total = day, 0
	}
}
