package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The footer drops from the tail: back and help go before open, since Esc
// and ? work everywhere and the footer is the only place open is named.
func TestScheduleHintsDropByPriority(t *testing.T) {
	t.Parallel()

	full := footerHints(scheduleHints(false), 80)
	narrow := footerHints(scheduleHints(false), 30)

	assert.Contains(t, full, "[?]help")
	assert.Contains(t, narrow, "[enter]open")
	assert.NotContains(t, narrow, "[?]help")
}
