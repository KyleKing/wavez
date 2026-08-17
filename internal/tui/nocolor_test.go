package tui_test

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/tui"
)

// colorEscapes returns the SGR parameters in s that set a foreground or
// background color. Attribute parameters (bold, faint, reverse) are not
// colors and must survive NO_COLOR: the transcript search highlights its
// hits with reverse video precisely so it works on a monochrome terminal.
func colorEscapes(s string) []string {
	var found []string

	for rest := s; ; {
		i := strings.Index(rest, "\x1b[")
		if i < 0 {
			return found
		}

		rest = rest[i+2:]

		end := strings.IndexFunc(rest, func(r rune) bool { return r < '0' || r > '9' && r != ';' })
		if end < 0 || rest[end] != 'm' {
			continue
		}

		found = append(found, colorParams(rest[:end])...)
		rest = rest[end:]
	}
}

func colorParams(seq string) []string {
	var found []string

	for _, p := range strings.Split(seq, ";") {
		n, err := strconv.Atoi(p)
		if err != nil {
			continue
		}

		switch {
		case n == 38 || n == 48,
			n >= 30 && n <= 37, n >= 40 && n <= 47,
			n >= 90 && n <= 97, n >= 100 && n <= 107:
			found = append(found, "\x1b["+seq+"m")

			return found
		}
	}

	return found
}

func pendingQuestion() api.Reply {
	return api.Reply{Kind: api.RepPending, Pending: []api.PendingInfo{
		{ID: "p1", ThreadID: "t2", Thread: "docs-pass", Tool: "ask", Action: "which lock?", Question: true},
	}}
}

func press(t *testing.T, m tui.Model, keys ...rune) tui.Model {
	t.Helper()

	for _, k := range keys {
		m = apply(t, m, tea.KeyPressMsg{Code: k, Text: string(k)})
	}

	return m
}

// inputScreen is one screen under test, built at a given size with its text
// field either closed or open.
type inputScreen struct {
	build       func(t *testing.T, opts tui.Options, w, h int) tui.Model
	name        string
	wantReverse bool
}

// inputScreens covers every screen that owns a text field, with the field
// both closed and open, so a component style that ignores the theme shows up
// in exactly one of the pair.
func inputScreens() []inputScreen {
	return []inputScreen{
		{name: "home", build: func(t *testing.T, opts tui.Options, w, h int) tui.Model {
			t.Helper()

			return apply(t, newSized(t, opts, w, h), api.Reply{Kind: api.RepThreads, Threads: sampleThreads()})
		}},
		{name: "home_filter_open", build: func(t *testing.T, opts tui.Options, w, h int) tui.Model {
			t.Helper()

			m := apply(t, newSized(t, opts, w, h), api.Reply{Kind: api.RepThreads, Threads: sampleThreads()})

			return press(t, m, '/')
		}},
		{name: "home_answer_open", build: func(t *testing.T, opts tui.Options, w, h int) tui.Model {
			t.Helper()

			m := apply(t, newSized(t, opts, w, h),
				api.Reply{Kind: api.RepThreads, Threads: sampleThreads()}, pendingQuestion())

			return press(t, m, 'v', 'y')
		}},
		{name: "inbox_answer_open", build: func(t *testing.T, opts tui.Options, w, h int) tui.Model {
			t.Helper()

			m := apply(t, newSized(t, opts, w, h),
				api.Reply{Kind: api.RepThreads, Threads: sampleThreads()}, pendingQuestion())

			return press(t, m, 'i', 'y')
		}},
		{name: "new_thread_form", build: func(t *testing.T, opts tui.Options, w, h int) tui.Model {
			t.Helper()

			return press(t, newSized(t, opts, w, h), 'n')
		}},
		{name: "palette_open", build: func(t *testing.T, opts tui.Options, w, h int) tui.Model {
			t.Helper()

			m := apply(t, newSized(t, opts, w, h), api.Reply{Kind: api.RepThreads, Threads: sampleThreads()})

			return press(t, m, ':')
		}},
		{name: "thread", build: func(t *testing.T, opts tui.Options, w, h int) tui.Model {
			t.Helper()

			return searchFixture(t, opts, w, h)
		}},
		{name: "thread_input_focused", build: func(t *testing.T, opts tui.Options, w, h int) tui.Model {
			t.Helper()

			m := searchFixture(t, opts, w, h)

			return apply(t, m, tea.KeyPressMsg{Code: tea.KeyTab}, tea.KeyPressMsg{Code: tea.KeyTab})
		}},
		{name: "thread_search_open", build: func(t *testing.T, opts tui.Options, w, h int) tui.Model {
			t.Helper()

			return press(t, searchFixture(t, opts, w, h), '/')
		}},
		{
			name: "thread_search_committed", wantReverse: true,
			build: func(t *testing.T, opts tui.Options, w, h int) tui.Model {
				t.Helper()

				return typeQuery(t, searchFixture(t, opts, w, h), "lease")
			},
		},
	}
}

// sizes spans DESIGN's 80x24 minimum, the last width at which the diff pane
// still stacks, and a wide terminal where it sits side by side.
var sizes = []struct{ w, h int }{{80, 24}, {99, 30}, {130, 32}}

func TestNoColor_NoColorEscapesOnAnyScreenWithInput(t *testing.T) {
	t.Parallel()

	for _, sc := range inputScreens() {
		for _, size := range sizes {
			t.Run(sc.name+"_"+strconv.Itoa(size.w), func(t *testing.T) {
				t.Parallel()

				out := sc.build(t, tui.Options{NoColor: true}, size.w, size.h).View().Content

				assert.Empty(t, colorEscapes(out), "NO_COLOR must not emit a color escape")

				if sc.wantReverse {
					assert.Contains(t, out, "\x1b[7m", "the search highlight must keep its reverse video")
				}
			})
		}
	}
}

func TestNoColor_AsciiGlyphs(t *testing.T) {
	t.Parallel()

	m := newSized(t, tui.Options{NoColor: true}, 100, 30)
	m = apply(t, m, api.Reply{Kind: api.RepThreads, Threads: sampleThreads()})

	out := m.View().Content

	assert.True(t, strings.ContainsAny(out, "!*x"), "needs-input/gate/failed rows must use ASCII glyphs")
	assert.NotContains(t, out, "▲", "unicode glyphs must not appear under NO_COLOR")
}

func TestColor_EmitsAnsiAndUnicodeGlyphs(t *testing.T) {
	t.Parallel()

	for _, sc := range inputScreens() {
		t.Run(sc.name, func(t *testing.T) {
			t.Parallel()

			out := sc.build(t, tui.Options{NoColor: false}, 100, 30).View().Content

			require.NotEmpty(t, colorEscapes(out), "a color-capable render styles more than attributes")
		})
	}

	m := newSized(t, tui.Options{NoColor: false}, 100, 30)
	m = apply(t, m, api.Reply{Kind: api.RepThreads, Threads: sampleThreads()})

	assert.Contains(t, m.View().Content, "▲", "needs-input should render its unicode glyph outside NO_COLOR")
}
