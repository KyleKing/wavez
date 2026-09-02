package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"

	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/tui"
)

func samplePending() []api.PendingInfo {
	return []api.PendingInfo{
		{
			ID: "p1", ThreadID: "t2", Thread: "docs-pass", Tool: "shell",
			Action: "run", Step: "editing internal/lease.go",
			Detail: "cat > NEXT_STEPS.md <<'EOF'\n# Next Steps\nEOF",
			Reason: "cat is not on the list of commands that run without asking",
		},
		{
			ID: "p2", ThreadID: "t7", Thread: "add-jj-backend", Question: true,
			Detail: "colocate or pure jj?",
		},
	}
}

func openInbox(t *testing.T, width, height int) tui.Model {
	t.Helper()

	m := newSized(t, tui.Options{Dir: "~/dev", NoColor: true}, width, height)

	return apply(t, m,
		api.Reply{Kind: api.RepPending, Pending: samplePending()},
		tea.KeyPressMsg{Code: 'i', Text: "i"},
	)
}

// A parked row names what it parked on, distinct from the prompt itself:
// Step is the last thing the thread was doing, Detail/Action is the prompt
// now blocking it.
func TestInbox_RowShowsWhatTheThreadParkedOn(t *testing.T) {
	t.Parallel()

	out := openInbox(t, 80, 24).View().Content

	assert.Contains(t, out, "docs-pass")
	assert.Contains(t, out, "editing internal/lease.go")
	// The command being approved, folded onto one line: a row that named only
	// the reason asked the user to approve something they could not see.
	assert.Contains(t, out, "cat > NEXT_STEPS.md <<'EOF' # Next Steps EOF")
}

// A question prompt carries no Step of its own (Detail is the question
// text), so a thread parked with none adds no extra muted line for it.
func TestInbox_RowWithNoStepAddsNoExtraLine(t *testing.T) {
	t.Parallel()

	out := openInbox(t, 80, 24).View().Content

	assert.Equal(t, 1, strings.Count(out, "editing internal/lease.go"))
	assert.Contains(t, out, "colocate or pu")
}

func TestInbox_GoldenFrame(t *testing.T) {
	t.Parallel()

	goldenCompare(t, "inbox_frame_80x24", openInbox(t, 80, 24).View().Content)
}
