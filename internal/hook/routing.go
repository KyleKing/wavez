package hook

import (
	"context"
	"time"

	"github.com/kyleking/wavez/internal/route"
)

// routeBudget bounds one route decision, whatever the decision model's own
// timeout says. A prompt waits on this hook, so the bound is short.
const routeBudget = 5 * time.Second

// prompted answers a UserPromptSubmit event. With the route hint on, the
// prompt is routed by the rules, then by the decision model, and a route
// other than the fallback becomes one fixed sentence beside the prompt. It
// has no way to fail: a decision that cannot be made is the fallback, and
// the fallback is silence, so this hook never blocks or delays a prompt
// past its budget.
func (h *Handler) prompted(ctx context.Context, ev Event, rec *record) output {
	if h.router == nil || ev.Prompt == "" {
		return output{}
	}

	ctx, cancel := context.WithTimeout(ctx, routeBudget)
	defer cancel()

	got := route.Decide(ctx, h.router, rec.Root, ev.Prompt)
	rec.Note = "route " + got.Choice + " by " + got.Backend

	hint := route.Hint(got.Choice)
	if hint == "" {
		return output{}
	}

	return contextFor(eventUserPromptSubmit, hint)
}
