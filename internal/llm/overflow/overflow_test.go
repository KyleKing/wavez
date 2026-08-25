package overflow_test

import (
	"context"
	"testing"

	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/llm/fake"
	"github.com/kyleking/wavez/internal/llm/overflow"
)

// The pick is per turn rather than per run, because what makes a local turn
// slow is a gate run that starts in the middle of a thread. A tier with
// nowhere to overflow to stays local however the machine reads.
func TestProvider_PicksPerTurn(t *testing.T) {
	t.Parallel()

	turn := func() fake.Turn {
		return fake.Turn{Text: []string{"ok"}, StopReason: llm.StopEndTurn}
	}

	local := fake.New("local", turn(), turn(), turn())
	hosted := fake.New("hosted", turn(), turn(), turn())

	busy := false
	p := overflow.New("fast", local, hosted, func(context.Context) bool { return busy })

	drain := func() {
		t.Helper()
		for _, err := range p.Stream(context.Background(), llm.Request{Model: "m"}) {
			if err != nil {
				t.Fatalf("Stream: %v", err)
			}
		}
	}

	drain()
	busy = true

	drain()
	busy = false

	drain()

	if got := len(local.Requests()); got != 2 {
		t.Errorf("local served %d turns, want the two the machine had room for", got)
	}

	if got := len(hosted.Requests()); got != 1 {
		t.Errorf("hosted served %d turns, want the one taken while the machine was busy", got)
	}

	only := fake.New("only", turn())
	if name := overflow.New("fast", only, nil, nil).Name(); name != "fast" {
		t.Errorf("Name = %q, want the tier it serves", name)
	}

	alone := overflow.New("fast", only, nil, nil)
	//nolint:revive // draining the stream is what sends the request
	for range alone.Stream(context.Background(), llm.Request{Model: "m"}) {
	}

	if got := len(only.Requests()); got != 1 {
		t.Errorf("a tier with nowhere to overflow to served %d turns locally, want 1", got)
	}
}
