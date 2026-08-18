package tui_test

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/tui"
)

func composeKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl} }

// focusComposer Tabs around Thread view's panels until the composer holds
// focus, which is when it reports insert mode.
func focusComposer(t *testing.T, m tui.Model) tui.Model {
	t.Helper()

	for range panelCycle {
		if strings.HasPrefix(composerLine(t, m), "INS") {
			return m
		}

		m = apply(t, m, tea.KeyPressMsg{Code: tea.KeyTab})
	}

	t.Fatal("tabbing never reached the composer")

	return m
}

// panelCycle is one full trip around Thread view's panels, plus the press
// that lands back on the composer.
const panelCycle = 4

// typeKeys sends a literal key sequence: runes as themselves, and the
// two-character escapes `<esc>` and `<cr>` as those keys, so a test reads
// like the keystrokes a user makes.
func typeKeys(t *testing.T, m tui.Model, keys string) tui.Model {
	t.Helper()

	for keys != "" {
		switch {
		case strings.HasPrefix(keys, "<esc>"):
			m, keys = apply(t, m, tea.KeyPressMsg{Code: tea.KeyEscape}), keys[len("<esc>"):]
		case strings.HasPrefix(keys, "<cr>"):
			m, keys = apply(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}), keys[len("<cr>"):]
		default:
			r, size := utf8.DecodeRuneInString(keys)
			m, keys = apply(t, m, tea.KeyPressMsg{Code: r, Text: string(r)}), keys[size:]
		}
	}

	return m
}

// composerLine is the composer's inline row, stripped of styling and of the
// frame around it.
func composerLine(t *testing.T, m tui.Model) string {
	t.Helper()

	for _, line := range strings.Split(m.View().Content, "\n") {
		plain := stripANSI(line)
		if strings.Contains(plain, "NOR") || strings.Contains(plain, "INS") {
			return strings.TrimSpace(strings.Trim(plain, "│"))
		}
	}

	t.Fatal("no composer row rendered")

	return ""
}

func stripANSI(s string) string {
	var b strings.Builder

	for s != "" {
		i := strings.Index(s, "\x1b[")
		if i < 0 {
			b.WriteString(s)

			break
		}

		b.WriteString(s[:i])

		end := strings.IndexByte(s[i:], 'm')
		if end < 0 {
			break
		}

		s = s[i+end+1:]
	}

	return b.String()
}

func composerFixture(t *testing.T, width, height int) tui.Model {
	t.Helper()

	m := newSized(t, tui.Options{NoColor: true}, width, height)
	m = openThread(t, m, sampleThreads()[:1])

	return focusComposer(t, m)
}

// sizes the composer is exercised at: DESIGN's 80x24 minimum, the last
// width where the diff pane stacks, and a wide terminal.
func composerSizes() []struct{ w, h int } {
	return []struct{ w, h int }{{80, 24}, {99, 30}, {130, 32}}
}

// TestComposer_MotionsAndEdits drives each binding as a keystroke sequence
// and reads the result off the rendered row, so a test fails the way the
// user would see it fail.
func TestComposer_MotionsAndEdits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		keys string
		want string
	}{
		{name: "insert then escape to normal", keys: "hello<esc>", want: "NOR hello"},
		{name: "a appends after the cursor", keys: "ab<esc>0aX", want: "INS aXb"},
		{name: "A appends at the end", keys: "ab<esc>0AZ", want: "INS abZ"},
		{name: "I inserts at the first non-blank", keys: "ab<esc>IZ", want: "INS Zab"},
		{name: "o opens a line below", keys: "one<esc>otwo", want: "INS 2/2 two"},
		{name: "O opens a line above", keys: "one<esc>Otwo", want: "INS 1/2 two"},
		{name: "x deletes under the cursor", keys: "abc<esc>0x", want: "NOR bc"},
		{name: "D deletes to the end of the line", keys: "one two<esc>0wD", want: "NOR one"},
		{name: "C changes to the end of the line", keys: "one two<esc>0wCsix", want: "INS one six"},
		{name: "dd deletes the line", keys: "one<esc>otwo<esc>dd", want: "NOR one"},
		{name: "dw deletes a word", keys: "one two<esc>0dw", want: "NOR two"},
		{name: "cw changes to the end of the word", keys: "one two<esc>0cwsix", want: "INS six two"},
		{name: "d$ deletes to the end", keys: "one two<esc>0ld$", want: "NOR o"},
		{name: "db deletes back a word", keys: "one two<esc>db", want: "NOR one o"},
		{name: "de deletes to the word end", keys: "one two<esc>0de", want: "NOR  two"},
		{name: "w b e step words", keys: "one two six<esc>0wwbex", want: "NOR one tw six"},
		{name: "0 and $ jump the line ends", keys: "abcd<esc>0x$x", want: "NOR bc"},
		{name: "h and l step characters", keys: "abcd<esc>0llx", want: "NOR abd"},
		{name: "j and k step lines keeping the column", keys: "one<esc>otwo<esc>kx", want: "NOR 1/2 on"},
		{name: "gg and G jump the buffer ends", keys: "one<esc>otwo<esc>ggx", want: "NOR 1/2 ne"},
		{name: "G jumps to the last line", keys: "one<esc>otwo<esc>ggGx", want: "NOR 2/2 wo"},
		{name: "u undoes the last edit", keys: "abc<esc>ddu", want: "NOR abc"},
		{name: "u undoes a whole insert session", keys: "abc<esc>Adef<esc>u", want: "NOR abc"},
		{name: "p pastes what x cut", keys: "ab<esc>0xp", want: "NOR ba"},
		{name: "P pastes before the cursor", keys: "ab<esc>0x$P", want: "NOR ab"},
		{name: "dd then p restores the line below", keys: "one<esc>otwo<esc>ggddp", want: "NOR 2/2 one"},
		{name: "an unfinished operator is dropped", keys: "abc<esc>0dzx", want: "NOR bc"},
	}

	for _, tc := range tests {
		for _, size := range composerSizes() {
			t.Run(tc.name+"_"+strconv.Itoa(size.w), func(t *testing.T) {
				t.Parallel()

				m := typeKeys(t, composerFixture(t, size.w, size.h), tc.keys)
				assert.Equal(t, tc.want, composerLine(t, m))
			})
		}
	}
}

// Focusing the composer starts it in insert mode, and Esc walks back out one
// level at a time without ever quitting.
func TestComposer_EscapeLadderNeverQuits(t *testing.T) {
	t.Parallel()

	m := composerFixture(t, 100, 30)
	require.Contains(t, composerLine(t, m), "INS")

	m = typeKeys(t, m, "hi<esc>")
	assert.Contains(t, composerLine(t, m), "NOR", "esc leaves insert mode")

	m = apply(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.Contains(t, m.View().Content, "[d]diff", "esc from normal mode hands focus back")
	assert.Contains(t, m.View().Content, "hi", "the draft survives losing focus")

	m = apply(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.Contains(t, m.View().Content, "[enter]open", "esc returns to Home")
}

// Esc out of every composer level in turn, against a running program, so
// the ladder is proved not to reach tea.Quit at any rung.
func TestComposer_EscFromEveryLevelNeverQuits(t *testing.T) {
	t.Parallel()

	tm := teatest.NewTestModel(t, tui.New(tui.Options{}), teatest.WithInitialTermSize(100, 30))
	tm.Send(api.Reply{Kind: api.RepThreads, Threads: sampleThreads()[:1]})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	tm.Send(composeKey())

	for range 5 {
		tm.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
	}

	timedOut := false
	tm.WaitFinished(t,
		teatest.WithFinalTimeout(shortPoll),
		teatest.WithTimeoutFn(func(testing.TB) { timedOut = true }),
	)

	if !timedOut {
		t.Fatal("esc quit the program from the composer; it must only ever go back one level")
	}
}

// The same letter is a verb from the transcript and a character in the
// composer. `d` is the pair that would hurt most to get wrong.
func TestComposer_LetterIsVerbOrCharacterByFocus(t *testing.T) {
	t.Parallel()

	for _, size := range composerSizes() {
		t.Run(strconv.Itoa(size.w), func(t *testing.T) {
			t.Parallel()

			m := newSized(t, tui.Options{NoColor: true}, size.w, size.h)
			m = openThread(t, m, sampleThreads()[:1])
			m = apply(t, m, api.Reply{Kind: api.RepDiff, Diff: &api.Diff{ThreadID: "t1", Unified: sampleUnified()}})

			m = press(t, m, 'd')
			assert.Contains(t, m.View().Content, "› ", "d from the transcript opens the diff pane")

			m = typeKeys(t, focusComposer(t, m), "one two<esc>0dw")
			assert.Equal(t, "NOR two", composerLine(t, m), "d in the composer deletes")
		})
	}
}

// A permission prompt answers from the transcript panel, never from the
// composer, where `a` appends instead of granting allow-always.
func TestComposer_DoesNotAnswerPermissionPrompts(t *testing.T) {
	t.Parallel()

	m := newSized(t, tui.Options{NoColor: true}, 100, 30)
	m = openThread(t, m, []api.ThreadInfo{
		{ID: "t1", Name: "docs-pass", Dir: "calcipy", State: event.StateNeedsIn},
	})
	m = apply(t, m, api.Reply{Kind: api.RepPending, Pending: []api.PendingInfo{
		{ID: "p1", ThreadID: "t1", Tool: "shell", Action: "rm -rf .testmondata"},
	}})

	m = typeKeys(t, focusComposer(t, m), "<esc>a")
	assert.Contains(t, composerLine(t, m), "INS", "a in the composer starts an append")
}

func TestComposer_FullscreenRoundTrip(t *testing.T) {
	t.Parallel()

	for _, size := range composerSizes() {
		t.Run(strconv.Itoa(size.w), func(t *testing.T) {
			t.Parallel()

			m := composerFixture(t, size.w, size.h)
			m = typeKeys(t, m, "first<esc>")

			m = apply(t, m, composeKey())
			assert.Contains(t, m.View().Content, "-- NORMAL --", "expanding keeps the mode")

			m = typeKeys(t, m, "A")
			out := m.View().Content
			assert.Contains(t, out, "compose · fix-lock-timeout", "the frame is the composer")
			assert.Contains(t, out, "-- INSERT --", "the mode is on the status line")
			assert.NotContains(t, out, "ledger", "the thread frame is gone")

			m = typeKeys(t, m, "<cr>second")
			assert.Contains(t, m.View().Content, "ln 2/2", "enter opens a line in fullscreen")

			m = apply(t, m, composeKey())
			out = m.View().Content
			assert.Contains(t, out, "ledger", "the thread frame is back")
			assert.Equal(t, "INS 2/2 second", composerLine(t, m))
		})
	}
}

// Fullscreen is reachable from any panel and Esc unwinds it before it
// unwinds the screen.
func TestComposer_FullscreenFromTranscriptAndBack(t *testing.T) {
	t.Parallel()

	m := newSized(t, tui.Options{NoColor: true}, 100, 30)
	m = openThread(t, m, sampleThreads()[:1])

	m = apply(t, m, composeKey())
	require.Contains(t, m.View().Content, "-- INSERT --")

	m = apply(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.Contains(t, m.View().Content, "-- NORMAL --", "esc leaves insert first")

	m = apply(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})
	assert.Contains(t, m.View().Content, "ledger", "esc leaves fullscreen next")
}

func sampleUnified() string {
	return "diff --git a/internal/lease/lease.go b/internal/lease/lease.go\n" +
		"--- a/internal/lease/lease.go\n" +
		"+++ b/internal/lease/lease.go\n" +
		"@@ -1,3 +1,3 @@\n" +
		" package lease\n" +
		"-const DefaultTTL = 30\n" +
		"+const TTL = 30\n"
}
