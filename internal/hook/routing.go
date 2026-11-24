package hook

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/kyleking/wavez/internal/lookup"
	"github.com/kyleking/wavez/internal/route"
)

// routeBudget bounds one route decision, whatever the decision model's own
// timeout says. A prompt waits on this hook, so the bound is short.
const routeBudget = 5 * time.Second

// locateBudget bounds the lookup of the names in a prompt.
const locateBudget = 1500 * time.Millisecond

// prompted answers a UserPromptSubmit event with what wavez can say before
// the model starts: where the identifiers the prompt names are, when the
// project has that on, and the route hint, when it has that on. It has no
// way to fail. A lookup that finds nothing and a decision that cannot be
// made are both silence, so this hook never blocks a prompt or holds it past
// its budgets.
func (h *Handler) prompted(ctx context.Context, ev Event, rec *record) output {
	if ev.Prompt == "" {
		return output{}
	}

	var parts []string

	if h.locate {
		bounded, cancel := context.WithTimeout(ctx, locateBudget)
		sites := lookup.Find(bounded, rec.Root, ev.Prompt)

		cancel()

		hint := lookup.Hint(sites)
		if h.brief {
			hint = lookup.Brief(rec.Root, sites)
		}

		if hint != "" {
			rec.Note = "located " + strconv.Itoa(len(sites))
			parts = append(parts, hint)
		}
	}

	if hint := h.routed(ctx, ev, rec); hint != "" {
		parts = append(parts, hint)
	}

	if len(parts) == 0 {
		return output{}
	}

	return contextFor(eventUserPromptSubmit, strings.Join(parts, "\n"))
}

// routed is the route hint for a prompt, and "" with the hint off or for
// the fallback route. The prompt is routed by the rules, then by the
// decision model.
func (h *Handler) routed(ctx context.Context, ev Event, rec *record) string {
	if h.router == nil {
		return ""
	}

	ctx, cancel := context.WithTimeout(ctx, routeBudget)
	defer cancel()

	got := route.Decide(ctx, h.router, rec.Root, ev.Prompt)
	rec.Note = strings.TrimSpace(rec.Note + " route " + got.Choice + " by " + got.Backend)

	return route.Hint(got.Choice)
}
