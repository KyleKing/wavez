package daemon

import (
	"sync"
	"time"

	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/router"
)

// usage totals one thread's token counts, read back off its own event log
// rather than kept alongside it: thread.AppendAssistant already records each
// turn's llm.Usage, so the daemon has the numbers without the agent loop
// reporting them a second time.
type usage struct {
	// timings is the last turn's runtime measurement, nil while no provider
	// has reported one. Decode speed and prefix reuse have no other source.
	timings   *llm.Timings
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

	if v.Timings != nil {
		u.timings = v.Timings
	}
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
			Timings:         timingsFromJSON(v["timings"]),
		}, true
	default:
		return llm.Usage{}, false
	}
}

func timingsFromJSON(raw any) *llm.Timings {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}

	return &llm.Timings{
		PromptTokens:    jsonInt(m, "prompt_tokens"),
		CachedTokens:    jsonInt(m, "cached_tokens"),
		PromptPerSecond: jsonFloat(m, "prompt_per_second"),
		DecodePerSecond: jsonFloat(m, "decode_per_second"),
	}
}

func jsonInt(m map[string]any, key string) int {
	return int(jsonFloat(m, key))
}

func jsonFloat(m map[string]any, key string) float64 {
	f, ok := m[key].(float64)
	if !ok {
		return 0
	}

	return f
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

// servedFromEvent reads which model and tier answered a turn off the same
// KindAgent event that carries its usage. A thread pins a tier at most, so
// this is the only record of what a turn actually ran on. The tier is
// recorded even where the tier's model has no configured name, so either
// one alone is an answer.
func servedFromEvent(ev event.Event) (string, router.Choice, bool) {
	if ev.Kind != event.KindAgent {
		return "", "", false
	}

	model, hasModel := ev.Detail["model"].(string)

	tier, hasTier := ev.Detail["tier"].(string)
	if !hasModel && !hasTier {
		return "", "", false
	}

	return model, router.Choice(tier), true
}
