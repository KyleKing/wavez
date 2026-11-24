package hook

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/kyleking/wavez/internal/session"
)

// sourceCompact is the SessionStart source of a session whose conversation
// was just replaced by a summary of it.
const sourceCompact = "compact"

const (
	// The recallFiles bound is how many changed files are named after a compaction.
	recallFiles = 12
	// The recallBudget time bounds reading the goal for the lines.
	recallBudget = 3 * time.Second
)

// recalled is what wavez knows of a session for certain, said again when
// the conversation has been compacted: which files the session changed,
// whether the checks passed on them as they are, and how far the goal is.
// A summary carries these loosely or not at all, and a session that lost
// them spends turns finding them out again. It is a few lines of fixed
// shape, and "" for a session that changed nothing and has no goal.
func (h *Handler) recalled(ctx context.Context, store *session.Store, ev Event, rec *record) string {
	if ev.Source != sourceCompact {
		return ""
	}

	state, ok := store.Read(ev.SessionID)
	if !ok {
		return ""
	}

	var lines []string

	if changed, id, err := changeSet(ctx, store, rec.Root, state); err == nil && len(changed) > 0 {
		shown := changed[:min(len(changed), recallFiles)]
		line := "Files this session changed: " + strings.Join(shown, ", ")

		if more := len(changed) - len(shown); more > 0 {
			line += ", and " + strconv.Itoa(more) + " more"
		}

		verdict := "They have not been checked as they are now."
		if id == state.Passed {
			verdict = "The checks passed on them as they are now."
		}

		lines = append(lines, line+".", verdict)
	}

	goalCtx, cancel := context.WithTimeout(ctx, recallBudget)
	defer cancel()

	if person, _, _, err := h.goals(goalCtx, rec.Root); err == nil && person != "" {
		lines = append(lines, "Goal: "+person)
	}

	if len(lines) == 0 {
		return ""
	}

	rec.Note = strings.TrimSpace(rec.Note + " recalled after compaction")

	return "wavez kept this across the compaction:\n" + strings.Join(lines, "\n")
}
