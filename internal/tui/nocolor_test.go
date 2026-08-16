package tui_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/tui"
)

func TestNoColor_NoAnsiEscapesAndAsciiGlyphs(t *testing.T) {
	t.Parallel()

	m := newSized(t, tui.Options{NoColor: true}, 100, 30)
	m = apply(t, m, api.Reply{Kind: api.RepThreads, Threads: sampleThreads()})

	out := m.View().Content

	// NO_COLOR forbids color escapes (38;/48;) but not the bold/faint SGR
	// attributes the design uses in their place for focus and hierarchy.
	assert.NotContains(t, out, "38;", "NO_COLOR must not emit a foreground color escape")
	assert.NotContains(t, out, "48;", "NO_COLOR must not emit a background color escape")
	assert.True(t, strings.ContainsAny(out, "!*x"), "needs-input/gate/failed rows must use ASCII glyphs")
	assert.NotContains(t, out, "▲", "unicode glyphs must not appear under NO_COLOR")
}

func TestColor_EmitsAnsiAndUnicodeGlyphs(t *testing.T) {
	t.Parallel()

	m := newSized(t, tui.Options{NoColor: false}, 100, 30)
	m = apply(t, m, api.Reply{Kind: api.RepThreads, Threads: sampleThreads()})

	out := m.View().Content

	assert.Contains(t, out, "\x1b[", "a color-capable render should style at least the border")
	assert.Contains(t, out, "▲", "needs-input should render its unicode glyph outside NO_COLOR")
}
