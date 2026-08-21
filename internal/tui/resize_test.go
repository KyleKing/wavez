package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"

	"github.com/kyleking/wavez/internal/tui"
)

func TestMinimumSize_RendersAt80x24(t *testing.T) {
	t.Parallel()

	m := newSized(t, tui.Options{}, 80, 24)
	out := m.View().Content

	assert.NotContains(t, out, "wavez needs 80x24")
}

func TestMinimumSize_BelowMinimumShowsMessage(t *testing.T) {
	t.Parallel()

	m := newSized(t, tui.Options{}, 79, 23)
	out := m.View().Content

	assert.Contains(t, out, "wavez needs 80x24")
	assert.Contains(t, out, "79x23")

	for _, line := range strings.Split(out, "\n") {
		assert.LessOrEqual(t, len(line), 79, "the message has to fit the terminal it is complaining about")
	}
}

func TestResize_MidSessionReflowsWidth(t *testing.T) {
	t.Parallel()

	m := newSized(t, tui.Options{}, 80, 24)
	narrow := m.View().Content

	m = apply(t, m, tea.WindowSizeMsg{Width: 140, Height: 40})
	wide := m.View().Content

	assert.NotEqual(t, len(narrow), len(wide))

	m = apply(t, m, tea.WindowSizeMsg{Width: 79, Height: 23})
	assert.Contains(t, m.View().Content, "wavez needs 80x24")
}
