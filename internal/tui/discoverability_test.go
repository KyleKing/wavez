package tui_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/tui"
)

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plainText(rendered string) string {
	return ansiPattern.ReplaceAllString(rendered, "")
}

func homeHelp(t *testing.T, width, height int) tui.Model {
	t.Helper()

	return apply(t, newSized(t, tui.Options{NoColor: true}, width, height), pressHelp)
}

// threadHelp exercises the screen with the most hints and the only composer
// section, which is where the list first outgrew the terminal.
func threadHelp(t *testing.T, width, height int) tui.Model {
	t.Helper()

	m := openThread(t, newSized(t, tui.Options{NoColor: true}, width, height), sampleThreads()[:1])

	return apply(t, m, pressHelp)
}

var pressHelp = tea.KeyPressMsg{Code: '?', Text: "?"}

// eachScreen opens every screen the app can show with enough content to be
// taller than the terminal if its lists are not windowed: a long lane list for
// the schedule and the thread list Home feeds it.
func eachScreen(t *testing.T, width, height int) tui.Model {
	t.Helper()

	lanes := make([]api.Lane, 0, 64)
	for i := range 64 {
		lanes = append(lanes, api.Lane{
			ThreadID: fmt.Sprintf("t%d", i), Thread: fmt.Sprintf("create-the-file-%02d", i),
			Step: "editing internal/tui/schedule.go", Cells: cells(event.StateWorking),
		})
	}

	// A goal-derived thread name runs well past any column a layout reserves
	// for it, which is where a name column that takes the row's leftovers
	// stops leaving room for the rest of the row.
	lanes = append([]api.Lane{{
		ThreadID: "long", Thread: "read-internal-tools-write-go-and-report-what-the-schema-says",
		Step: "editing internal/tui/schedule.go", Cells: cells(event.StateWorking, event.StateDone),
	}}, lanes...)

	sched := scheduleReply()
	sched.Lanes = append(sched.Lanes, lanes...)

	m := newSized(t, tui.Options{Dir: "~/dev", NoColor: true}, width, height)
	m = apply(t, m,
		api.Reply{Kind: api.RepThreads, Threads: sampleThreads()},
		api.Reply{Kind: api.RepPending, Pending: samplePending()},
		api.Reply{Kind: api.RepSchedule, Schedule: sched},
	)

	return m
}

// keyPress builds one character key, the way the tests would otherwise spell
// tea.KeyPressMsg{Code: 'x', Text: "x"} a dozen times.
func keyPress(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

// screenAt walks to one screen with the keys the app itself binds. The route
// runs from a fresh model each time so screens do not stack on each other.
func screenAt(t *testing.T, name string, width, height int) tui.Model {
	t.Helper()

	m := eachScreen(t, width, height)

	switch name {
	case "home":
	case "thread":
		m = apply(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	case "schedule":
		m = apply(t, m, keyPress('s'))
	case "inbox":
		m = apply(t, m, keyPress('i'))
	case "diagnostics":
		m = apply(t, m, keyPress('D'))
	case "routines":
		m = apply(t, m, keyPress('R'), api.Reply{Kind: api.RepRoutines, Routines: sampleRoutines()})
	case "models":
		m = apply(t, m, keyPress('M'))
	case "summary":
		m = apply(t, m, tea.KeyPressMsg{Code: tea.KeyEnter}, keyPress('s'))
	}

	return m
}

// TestEveryScreen_FitsItsTerminal is the property that caught the schedule
// rendering every lane it had: whatever the screen, the frame never runs past
// the terminal's height, so the bottom rule and the footer stay on screen.
func TestEveryScreen_FitsItsTerminal(t *testing.T) {
	t.Parallel()

	screens := []string{"home", "thread", "schedule", "inbox", "diagnostics", "routines", "models", "summary"}

	for _, sz := range []struct{ width, height int }{{80, 24}, {100, 30}, {200, 50}} {
		for _, name := range screens {
			t.Run(fmt.Sprintf("%s/%dx%d", name, sz.width, sz.height), func(t *testing.T) {
				t.Parallel()

				out := plainText(screenAt(t, name, sz.width, sz.height).View().Content)

				if lines := strings.Split(out, "\n"); len(lines) > sz.height {
					t.Errorf("%s renders %d lines on a %d-line terminal:\n%s", name, len(lines), sz.height, out)
				}

				if !strings.Contains(out, "└") {
					t.Errorf("%s frame's bottom rule is missing:\n%s", name, out)
				}

				for i, line := range strings.Split(out, "\n") {
					if lipgloss.Width(line) > sz.width {
						t.Errorf("%s line %d is %d cells on a %d-column terminal: %q",
							name, i, lipgloss.Width(line), sz.width, line)
					}
				}
			})
		}
	}
}

func TestHelp_FitsTheTerminalOnEverySize(t *testing.T) {
	t.Parallel()

	// last names each screen's final hint, which is the one a layout that
	// runs off the bottom loses first and a clamp then hides.
	screens := []struct {
		name string
		open func(t *testing.T, w, h int) tui.Model
		last string
	}{
		{"home", homeHelp, "open the routines panel"},
		{"thread", threadHelp, "group rows by kind"},
	}

	for _, sz := range []struct{ width, height int }{{80, 24}, {100, 30}, {120, 34}, {200, 50}} {
		for _, sc := range screens {
			t.Run(fmt.Sprintf("%s/%dx%d", sc.name, sz.width, sz.height), func(t *testing.T) {
				t.Parallel()

				out := plainText(sc.open(t, sz.width, sz.height).View().Content)

				if lines := strings.Split(out, "\n"); len(lines) > sz.height {
					t.Errorf("help renders %d lines on a %d-line terminal:\n%s", len(lines), sz.height, out)
				}
				if !strings.Contains(out, "\u2514") {
					t.Errorf("help frame's bottom rule is missing:\n%s", out)
				}
				if !strings.Contains(out, sc.last) {
					t.Errorf("help dropped its last hint %q:\n%s", sc.last, out)
				}
			})
		}
	}
}

func TestHelp_ShowsPhrasesNotOneWordLabels(t *testing.T) {
	t.Parallel()

	m := threadHelp(t, 100, 30)
	help := plainText(m.View().Content)

	if !strings.Contains(help, "open the diff pane") {
		t.Errorf("help is missing a hint phrase:\n%s", help)
	}
	if strings.Contains(help, "  d  diff") {
		t.Errorf("help shows the footer's one-word label instead of the phrase:\n%s", help)
	}
}
