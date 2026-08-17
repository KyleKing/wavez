package daemon

import (
	"time"

	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/llm"
)

// StepTextForTest exposes stepText to the black-box test package.
func StepTextForTest(ev event.Event) string { return stepText(ev) }

// UsageFromEventForTest exposes usageFromEvent to the black-box test package.
func UsageFromEventForTest(ev event.Event) (llm.Usage, bool) { return usageFromEvent(ev) }

// SpendLedgerForTest exposes the hosted-spend ledger to the black-box test
// package, driven by an injected clock so a day boundary needs no waiting.
type SpendLedgerForTest struct{ ledger *spendLedger }

// NewSpendLedgerForTest builds a ledger reading now for its day boundary.
func NewSpendLedgerForTest(now func() time.Time) SpendLedgerForTest {
	return SpendLedgerForTest{ledger: newSpendLedger(now)}
}

// Add records hosted spend.
func (s SpendLedgerForTest) Add(usd float64) { s.ledger.add(usd) }

// Today reports the current day's hosted spend.
func (s SpendLedgerForTest) Today() float64 { return s.ledger.today() }
