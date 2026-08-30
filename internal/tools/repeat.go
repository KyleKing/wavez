package tools

import (
	"sync"

	"github.com/kyleking/wavez/internal/tool"
)

// repeatGuard remembers the refusals one run has already been given, so a
// call whose outcome was settled before it ran can be told so.
//
// The loop's own repeat detection reaches only a call that immediately
// follows its twin, and the re-sends that cost turns here are separated by
// a read, an undo, or another edit: 19 of the recorded failures since
// 2026-08-26 repeat an input that had already failed in the same run, of
// which that detection reached 4.
//
// The state a key is recorded against is what makes this a check rather
// than a guess. It has to be something the tool observed, so that an
// unchanged state proves the answer cannot have changed either, and a run
// that fixed the thing and tried again is a different call and is left
// alone.
type repeatGuard struct {
	seen map[string][32]byte
	mu   sync.Mutex
}

func newRepeatGuard() *repeatGuard { return &repeatGuard{seen: map[string][32]byte{}} }

// Repeated records key against state and reports whether the same pair was
// recorded before.
func (g *repeatGuard) Repeated(key string, state [32]byte) bool {
	g.mu.Lock()
	defer g.mu.Unlock()

	was, seen := g.seen[key]
	g.seen[key] = state

	return seen && was == state
}

// leadWithRepeat puts what to do ahead of the refusal the caller has already
// read once, and marks the result a repeat so the counts can see it.
func leadWithRepeat(result tool.Result, lead string) tool.Result {
	result.Content = lead + "\n\n" + result.Content
	result.Cause = tool.CauseRepeat

	return result
}
