package tui_test

import (
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/tui"
)

func sampleThreads() []api.ThreadInfo {
	base := fixedNow().Add(-time.Hour)

	return []api.ThreadInfo{
		{
			ID: "t1", Name: "fix-lock-timeout", Dir: "calcipy", Step: "editing internal/lease.go",
			State: event.StateWorking, LastEvent: base.Add(2 * time.Minute),
		},
		{
			ID: "t2", Name: "docs-pass", Dir: "calcipy", Step: "allow? rm -rf .testmondata",
			State: event.StateNeedsIn, LastEvent: base.Add(40 * time.Second),
		},
		{
			ID: "t3", Name: "add-jj-backend", Dir: "wavez", Step: "gate test 4/7",
			State: event.StateGating, LastEvent: base.Add(time.Minute),
		},
		{
			ID: "t4", Name: "flaky-ci", Dir: "yak-shears", Step: "go test failed",
			State: event.StateFailed, LastEvent: base,
		},
		{
			ID: "t5", Name: "release-notes", Dir: "yak-shears", Step: "done",
			State: event.StateDone, LastEvent: base.Add(-time.Hour),
		},
	}
}

// fleetThreads spans two roots so fleet-scope tests exercise real grouping:
// calcipy sorts before wavez alphabetically, and calcipy's own two threads
// keep the needs-input-first, most-recent ordering within their group.
func fleetThreads() []api.ThreadInfo {
	base := fixedNow().Add(-time.Hour)

	return []api.ThreadInfo{
		{
			ID: "f1", Name: "fix-lock-timeout", Root: "/dev/calcipy", Step: "editing internal/lease.go",
			State: event.StateWorking, LastEvent: base.Add(2 * time.Minute),
		},
		{
			ID: "f2", Name: "docs-pass", Root: "/dev/calcipy", Step: "allow? rm -rf .testmondata",
			State: event.StateNeedsIn, LastEvent: base.Add(40 * time.Second),
		},
		{
			ID: "f3", Name: "add-jj-backend", Root: "/dev/wavez", Step: "gate test 4/7",
			State: event.StateGating, LastEvent: base.Add(time.Minute),
		},
	}
}

func TestHome_ScopedRenderingHasNoGroupHeaders(t *testing.T) {
	t.Parallel()

	m := newSized(t, tui.Options{Dir: "/dev/wavez", NoColor: true}, 100, 30)
	m = apply(t, m, api.Reply{Kind: api.RepThreads, Threads: fleetThreads()})

	out := m.View().Content

	assert.NotContains(t, out, "calcipy/")
	assert.NotContains(t, out, "wavez/")
	assert.Contains(t, out, "wavez", "the title still names the scoped repo")
}

func TestHome_ScopeTogglesFleetGroupingByRoot(t *testing.T) {
	t.Parallel()

	m := newSized(t, tui.Options{Dir: "/dev/wavez", NoColor: true}, 100, 30)
	m = apply(t, m, api.Reply{Kind: api.RepThreads, Threads: fleetThreads()})

	m = apply(t, m, tea.KeyPressMsg{Code: 'w', Text: "w"})
	out := m.View().Content

	assert.Contains(t, out, "wavez · /dev ·", "fleet scope is titled by the roots' common parent")
	assert.Contains(t, out, "calcipy/")
	assert.Contains(t, out, "wavez/")

	calcHeader := strings.Index(out, "calcipy/")
	wavezHeader := strings.Index(out, "wavez/")
	docsPass := strings.Index(out, "docs-pass")
	fixLock := strings.Index(out, "fix-lock-timeout")
	addJJ := strings.Index(out, "add-jj-backend")

	assert.Less(t, calcHeader, wavezHeader, "groups sort by root name, calcipy before wavez")
	assert.Less(t, calcHeader, docsPass)
	assert.Less(t, calcHeader, fixLock)
	assert.Less(t, wavezHeader, addJJ)
	assert.Less(t, docsPass, fixLock, "within a group, needs-input still sorts first")
}

func TestHome_FilterMatchesRootBasename(t *testing.T) {
	t.Parallel()

	m := newSized(t, tui.Options{Dir: "/dev/wavez", NoColor: true}, 100, 30)
	m = apply(t, m,
		api.Reply{Kind: api.RepThreads, Threads: fleetThreads()},
		tea.KeyPressMsg{Code: 'w', Text: "w"},
		tea.KeyPressMsg{Code: '/', Text: "/"},
	)
	m = apply(t, m, tea.KeyPressMsg{Code: 'c', Text: "c"}, tea.KeyPressMsg{Code: 'a', Text: "a"},
		tea.KeyPressMsg{Code: 'l', Text: "l"}, tea.KeyPressMsg{Code: 'c', Text: "c"})

	out := m.View().Content

	assert.Contains(t, out, "docs-pass")
	assert.Contains(t, out, "fix-lock-timeout")
	assert.NotContains(t, out, "add-jj-backend", "filtering by root basename should exclude the other group")
}

func TestHome_RendersEveryState(t *testing.T) {
	t.Parallel()

	for _, size := range []struct{ w, h int }{{80, 24}, {120, 40}} {
		m := newSized(t, tui.Options{Dir: "~/dev", NoColor: true}, size.w, size.h)
		m = apply(t, m, api.Reply{Kind: api.RepThreads, Threads: sampleThreads()})

		out := m.View().Content

		for _, want := range []string{"fix-lock-timeout", "docs-pass", "add-jj-backend", "flaky-ci", "release-notes"} {
			assert.Contains(t, out, want, "size %dx%d", size.w, size.h)
		}

		// The state is a word rather than a glyph, so the column reads
		// without a legend and a filter can narrow on what it says.
		for _, state := range []string{"working", "gating", "waiting", "failed", "done"} {
			assert.Contains(t, out, state, "size %dx%d missing state %q", size.w, size.h, state)
		}
	}
}

func TestHome_SortsNeedsInputFirst(t *testing.T) {
	t.Parallel()

	m := newSized(t, tui.Options{NoColor: true}, 100, 30)
	m = apply(t, m, api.Reply{Kind: api.RepThreads, Threads: sampleThreads()})

	out := m.View().Content

	needsInput := strings.Index(out, "docs-pass")
	working := strings.Index(out, "fix-lock-timeout")
	done := strings.Index(out, "release-notes")

	assert.Positive(t, needsInput)
	assert.Less(t, needsInput, working, "needs-input thread should render before a working thread")
	assert.Less(t, working, done, "more recent threads should render before older ones")
}

func TestHome_EmptyState(t *testing.T) {
	t.Parallel()

	m := newSized(t, tui.Options{}, 80, 24)
	out := m.View().Content

	assert.Contains(t, out, "no threads yet · press n to start one")
}

func TestHome_SingleThread(t *testing.T) {
	t.Parallel()

	m := newSized(t, tui.Options{NoColor: true}, 80, 24)
	m = apply(t, m, api.Reply{Kind: api.RepThreads, Threads: sampleThreads()[:1]})

	out := m.View().Content
	assert.Contains(t, out, "fix-lock-timeout")
	assert.Contains(t, out, "1 threads")
}

func TestHome_GoldenFrame(t *testing.T) {
	t.Parallel()

	m := newSized(t, tui.Options{Dir: "~/dev", NoColor: true}, 80, 24)
	m = apply(t, m, api.Reply{Kind: api.RepThreads, Threads: sampleThreads()})

	goldenCompare(t, "home_frame", m.View().Content)
}

// TestHome_PermissionRowAnsweredInline covers the expand-and-answer UI path;
// TestPermissionAnswer_SendsDecisionInline (internal_test.go) covers that
// the y/n/a keys actually dispatch the right decision to the client.
func TestHome_PermissionRowAnsweredInline(t *testing.T) {
	t.Parallel()

	m := newSized(t, tui.Options{NoColor: true}, 100, 30)
	m = apply(t, m,
		api.Reply{Kind: api.RepThreads, Threads: sampleThreads()},
		api.Reply{Kind: api.RepPending, Pending: []api.PendingInfo{
			{ID: "p1", ThreadID: "t2", Thread: "docs-pass", Tool: "shell", Action: "rm -rf .testmondata"},
		}},
	)

	m = apply(t, m, tea.KeyPressMsg{Code: 'v', Text: "v"})
	out := m.View().Content

	assert.Contains(t, out, "rm -rf .testmondata")
	assert.Contains(t, out, "[y]es [n]o [a]lways")
}

// TestHome_FilterStateTerm pins the `state:` term against the trap that
// motivated it: a done thread whose goal says failedEdit must stay out of
// `state:failed`, which a term falling back to text matching would catch.
func TestHome_FilterStateTerm(t *testing.T) {
	t.Parallel()

	base := fixedNow().Add(-time.Hour)
	threads := []api.ThreadInfo{
		{
			ID: "s1", Name: "rename-lock", Dir: "calcipy", Step: "editing internal/lease.go",
			State: event.StateWorking, LastEvent: base.Add(2 * time.Minute),
		},
		{
			ID: "s2", Name: "rename-docs", Dir: "yak-shears", Step: "gate test 4/7",
			State: event.StateFailed, LastEvent: base.Add(time.Minute),
		},
		{
			ID: "s3", Name: "docs-pass", Dir: "calcipy", Step: "allow? rm -rf .testmondata",
			State: event.StateNeedsIn, LastEvent: base.Add(40 * time.Second),
		},
		{
			ID: "s4", Name: "failed-edit", Dir: "yak-shears", Step: "done", Goal: "failedEdit",
			State: event.StateDone, LastEvent: base,
		},
	}

	tests := []struct {
		name string
		keys string
		want []string
	}{
		{
			name: "text alone still matches by name", keys: "rename",
			want: []string{"rename-lock", "rename-docs"},
		},
		{
			name: "state term narrows to that state alone", keys: "state:failed",
			want: []string{"rename-docs"},
		},
		{
			name: "state term and text narrow together", keys: "state:failed rename",
			want: []string{"rename-docs"},
		},
		{name: "state term naming no state matches nothing", keys: "state:bogus"},
		{
			name: "needs-input spells the state its wire name does not", keys: "state:needs-input",
			want: []string{"docs-pass"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newSized(t, tui.Options{Dir: "/dev/wavez", NoColor: true}, 100, 30)
			m = apply(t, m,
				api.Reply{Kind: api.RepThreads, Threads: threads},
				tea.KeyPressMsg{Code: '/', Text: "/"},
			)

			for _, r := range tt.keys {
				m = apply(t, m, tea.KeyPressMsg{Code: r, Text: string(r)})
			}

			out := m.View().Content

			for _, row := range tt.want {
				assert.Contains(t, out, row)
			}

			for _, row := range threads {
				if !slices.Contains(tt.want, row.Name) {
					assert.NotContains(t, out, row.Name, "query %q should have filtered this row out", tt.keys)
				}
			}
		})
	}
}

// TestHome_Selection is the selection set as one table: space toggles a
// row, `*` marks every filtered row and a second `*` clears, the status
// line counts what a bulk action would touch, and y/n/a answer every
// selected row with a prompt pending (an answered row drops its marker and
// its prompt line, so the screen agrees with what was sent).
func TestHome_Selection(t *testing.T) {
	t.Parallel()

	pending := []api.PendingInfo{
		{ID: "p1", ThreadID: "t2", Thread: "docs-pass", Tool: "shell", Action: "rm -rf .testmondata"},
		{ID: "p2", ThreadID: "t3", Thread: "add-jj-backend", Tool: "write", Action: "write internal/lease.go"},
	}

	tests := []struct {
		name    string
		setup   []api.Reply
		keys    string
		want    []string
		wantNot []string
	}{
		{
			name:  "space marks the cursor row, without hiding its cursor mark",
			setup: []api.Reply{{Kind: api.RepThreads, Threads: sampleThreads()}},
			keys:  "<sp>",
			want:  []string{"1 selected", ">*  docs-pass"},
		},
		{
			name:    "space twice returns the row to unmarked",
			setup:   []api.Reply{{Kind: api.RepThreads, Threads: sampleThreads()}},
			keys:    "<sp><sp>",
			want:    []string{"5 of 5 threads"},
			wantNot: []string{"selected", " * "},
		},
		{
			name:  "`*` marks every filtered row, the cursor row carrying both marks",
			setup: []api.Reply{{Kind: api.RepThreads, Threads: sampleThreads()}},
			keys:  "*",
			want:  []string{"5 selected", ">*  docs-pass", " *  fix-lock-timeout", " *  add-jj-backend"},
		},
		{
			name:    "a second `*` clears the selection",
			setup:   []api.Reply{{Kind: api.RepThreads, Threads: sampleThreads()}},
			keys:    "**",
			want:    []string{"5 of 5 threads"},
			wantNot: []string{"selected", " * "},
		},
		{
			name:    "`*` selects only what the filter shows once it is applied",
			setup:   []api.Reply{{Kind: api.RepThreads, Threads: sampleThreads()}},
			keys:    "/calcipy<cr>*",
			want:    []string{"2 of 5 threads", "2 selected", ">*  docs-pass", " *  fix-lock-timeout"},
			wantNot: []string{" *  add-jj-backend"},
		},
		{
			name:    "esc clears a selection",
			setup:   []api.Reply{{Kind: api.RepThreads, Threads: sampleThreads()}},
			keys:    "*<esc>",
			want:    []string{"5 of 5 threads"},
			wantNot: []string{"selected", " * "},
		},
		{
			name: "y answers every selected row with a prompt pending",
			setup: []api.Reply{
				{Kind: api.RepThreads, Threads: sampleThreads()},
				{Kind: api.RepPending, Pending: pending},
			},
			keys: "*y",
			want: []string{"3 selected"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newSized(t, tui.Options{Dir: "/dev/wavez", NoColor: true}, 120, 40)
			for _, r := range tt.setup {
				m = apply(t, m, r)
			}

			for _, k := range keyMsgs(tt.keys) {
				m = apply(t, m, k)
			}

			out := m.View().Content

			for _, want := range tt.want {
				assert.Contains(t, out, want)
			}

			for _, not := range tt.wantNot {
				assert.NotContains(t, out, not)
			}
		})
	}
}

// keyMsgs turns a literal key sequence into the messages a real terminal
// sends: `<sp>`, `<esc>`, `<cr>`, and `<tab>` as those keys, every other
// rune as itself.
func keyMsgs(keys string) []tea.KeyPressMsg {
	msgs := make([]tea.KeyPressMsg, 0, len(keys))

	for keys != "" {
		var msg tea.KeyPressMsg

		switch {
		case strings.HasPrefix(keys, "<sp>"):
			msg, keys = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}, keys[len("<sp>"):]
		case strings.HasPrefix(keys, "<esc>"):
			msg, keys = tea.KeyPressMsg{Code: tea.KeyEscape}, keys[len("<esc>"):]
		case strings.HasPrefix(keys, "<cr>"):
			msg, keys = tea.KeyPressMsg{Code: tea.KeyEnter}, keys[len("<cr>"):]
		case strings.HasPrefix(keys, "<tab>"):
			msg, keys = tea.KeyPressMsg{Code: tea.KeyTab}, keys[len("<tab>"):]
		default:
			r, size := utf8.DecodeRuneInString(keys)
			msg, keys = tea.KeyPressMsg{Code: r, Text: string(r)}, keys[size:]
		}

		msgs = append(msgs, msg)
	}

	return msgs
}
