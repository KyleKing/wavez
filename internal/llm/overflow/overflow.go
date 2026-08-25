// Package overflow serves a tier from the local model while the machine can
// keep up and from a second endpoint while it cannot. Decode rate on the
// local server is the laptop's to lose: one replay lane recorded 2.1 output
// tokens per second against a median of 25 because a gate run and a test
// suite were running beside it, which is a 12x swing in the thing a turn
// spends its whole wall clock on.
package overflow

import (
	"context"
	"iter"

	"github.com/kyleking/wavez/internal/llm"
)

// Busy reports whether the machine is too loaded to serve a turn here. It is
// consulted once per turn rather than per token, so a turn that starts local
// finishes local however the machine changes under it.
type Busy func(context.Context) bool

// Provider streams from local while Busy says the machine has room, and from
// elsewhere while it does not.
type Provider struct {
	local    llm.Provider
	elsewhen llm.Provider
	busy     Busy
	name     string
}

// New builds a Provider named for the tier it serves. A nil elsewhere or a
// nil busy is a caller with nowhere to overflow to, which streams from local
// always.
func New(name string, local, elsewhere llm.Provider, busy Busy) *Provider {
	return &Provider{name: name, local: local, elsewhen: elsewhere, busy: busy}
}

// Name implements llm.Provider.
func (p *Provider) Name() string { return p.name }

// Stream implements llm.Provider, choosing the endpoint before the request
// goes out.
func (p *Provider) Stream(ctx context.Context, req llm.Request) iter.Seq2[llm.Chunk, error] {
	return p.pick(ctx).Stream(ctx, req)
}

//nolint:ireturn // the whole point is returning whichever provider answers
func (p *Provider) pick(ctx context.Context) llm.Provider {
	if p.elsewhen == nil || p.busy == nil || !p.busy(ctx) {
		return p.local
	}

	return p.elsewhen
}
