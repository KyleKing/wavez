package tui_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

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
