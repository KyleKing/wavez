package daemon_test

import (
	"encoding/json"
	"math"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/daemon"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/llm"
	"github.com/kyleking/wavez/internal/llm/fake"
	"github.com/kyleking/wavez/internal/permission"
	"github.com/kyleking/wavez/internal/router"
)

// A live subscriber sees the *llm.Usage the thread appended; one replaying the
// log off disk sees the same value decoded from JSON. Both have to read.
func TestUsageFromEvent(t *testing.T) {
	t.Parallel()

	want := llm.Usage{InputTokens: 900, OutputTokens: 120, CacheReadTokens: 300}

	b, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal usage: %v", err)
	}

	var fromDisk map[string]any
	if err := json.Unmarshal(b, &fromDisk); err != nil {
		t.Fatalf("unmarshal usage: %v", err)
	}

	tests := []struct {
		detail map[string]any
		name   string
		want   llm.Usage
		wantOK bool
	}{
		{name: "live pointer", detail: map[string]any{"usage": &want}, want: want, wantOK: true},
		{name: "decoded from the log file", detail: map[string]any{"usage": fromDisk}, want: want, wantOK: true},
		{name: "stream ended without usage", detail: map[string]any{"tool_calls": 0}},
		{name: "no detail at all", detail: nil},
		{name: "nil usage", detail: map[string]any{"usage": (*llm.Usage)(nil)}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := daemon.UsageFromEventForTest(event.Event{Kind: event.KindAgent, Detail: tc.detail})
			if ok != tc.wantOK {
				t.Fatalf("usageFromEvent ok = %v, want %v", ok, tc.wantOK)
			}
			if got != tc.want {
				t.Fatalf("usageFromEvent = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestSpendLedger_TodayCoversOneDay(t *testing.T) {
	t.Parallel()

	day := time.Date(2026, time.August, 17, 22, 0, 0, 0, time.UTC)

	tests := []struct {
		name  string
		steps []time.Duration
		want  float64
	}{
		{name: "same day accumulates", steps: []time.Duration{0, time.Hour}, want: 0.06},
		{name: "next day starts over", steps: []time.Duration{0, 3 * time.Hour}, want: 0.03},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var offset atomic.Int64

			ledger := daemon.NewSpendLedgerForTest(func() time.Time {
				return day.Add(time.Duration(offset.Load()))
			})
			for _, step := range tc.steps {
				offset.Store(int64(step))
				ledger.Add(0.03)
			}

			if got := ledger.Today(); got != tc.want {
				t.Fatalf("Today = %v, want %v", got, tc.want)
			}
		})
	}
}

// The panel is a view over numbers the daemon already has, and a gauge it
// cannot measure has to arrive as absent rather than as a zero reading.
func TestDiagnostics_SeparatesReadingsFromUnmeasuredGauges(t *testing.T) {
	t.Parallel()

	t.Run("with no turn run yet", func(t *testing.T) {
		t.Parallel()

		h := newHarness(t, fake.New("local"))
		cl := dial(t, h)
		cl.hello()

		diag := fetchDiag(t, cl)
		gauges := []api.Gauge{
			api.GaugeCacheRead, api.GaugeMemory, api.GaugeModelBytes, api.GaugePrefixHit, api.GaugeTokensPerSec,
		}
		for _, gauge := range gauges {
			if diag.Measured(gauge) {
				t.Fatalf("%s reported as measured with nothing behind it: %+v", gauge, diag.Unmeasured)
			}
		}
	})

	t.Run("after a turn reports usage", func(t *testing.T) {
		t.Parallel()

		local := fake.New("local", fake.Turn{
			Text:       []string{"done"},
			StopReason: llm.StopEndTurn,
			Usage:      &llm.Usage{InputTokens: 1000, OutputTokens: 100, CacheReadTokens: 250},
		})
		h := newHarness(t, local)
		cl := dial(t, h)
		cl.hello()
		th := cl.newThread(nil)
		runOneTurn(t, cl, th.ID)

		diag := fetchDiag(t, cl)
		if !diag.Measured(api.GaugeCacheRead) || diag.CacheRead != 0.25 {
			t.Fatalf("CacheRead = %v (unmeasured %v), want 0.25 measured", diag.CacheRead, diag.Unmeasured)
		}
		if diag.Measured(api.GaugeTokensPerSec) || diag.Measured(api.GaugePrefixHit) {
			t.Fatalf("llama-server timings are not observable, yet reported as measured: %+v", diag.Unmeasured)
		}
		if diag.LocalModel == "" {
			t.Fatal("LocalModel is empty, want the model the router serves local turns with")
		}

		info := listThread(t, cl, th.ID)
		if info.Tokens != 1100 || info.Context != 1100 {
			t.Fatalf("thread tokens = %+v, want 1100 tokens and 1100 context", info)
		}
	})
}

// The window was the local tier's for every thread, so a run served on
// balanced in a 128k window rendered as 9.4k of an 8.2k context and read as
// overflowing. Only the turn's own log event says which tier answered it.
func TestThreadInfo_WindowFollowsTheTierThatServed(t *testing.T) {
	t.Parallel()

	local := fake.New("local", fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn})
	h := newHarness(t, local)
	cl := dial(t, h)
	cl.hello()
	th := cl.newThread(nil)

	if info := listThread(t, cl, th.ID); info.Tier != "" {
		t.Fatalf("Tier = %q before any turn, want empty", info.Tier)
	}

	runOneTurn(t, cl, th.ID)

	info := listThread(t, cl, th.ID)
	if info.Tier != router.Default {
		t.Fatalf("Tier = %q, want the tier that served the turn (%q)", info.Tier, router.Default)
	}

	if info.Window != router.HostedContextBudget {
		t.Fatalf("Window = %d for a turn served on %s, want %d",
			info.Window, info.Tier, router.HostedContextBudget)
	}
}

func fetchDiag(t *testing.T, cl *client) api.Diagnostics {
	t.Helper()

	cl.send(api.Command{ID: "diag", Kind: api.CmdDiag})

	rep := cl.recvFor("diag")
	if rep.Kind != api.RepDiag || rep.Diag == nil {
		t.Fatalf("diag reply = %+v", rep)
	}

	return *rep.Diag
}

func listThread(t *testing.T, cl *client, id string) api.ThreadInfo {
	t.Helper()

	cl.send(api.Command{ID: "list", Kind: api.CmdList})

	rep := cl.recvFor("list")
	idx := slices.IndexFunc(rep.Threads, func(info api.ThreadInfo) bool { return info.ID == id })
	if idx < 0 {
		t.Fatalf("thread %s missing from list %+v", id, rep.Threads)
	}

	return rep.Threads[idx]
}

// runOneTurn sends a prompt and returns once the thread has reported done, so
// an assertion never races the turn that produces the numbers.
func runOneTurn(t *testing.T, cl *client, id string) {
	t.Helper()

	cl.send(api.Command{ID: "sub", Kind: api.CmdSubscribe, ThreadID: id})
	if _, ok := cl.recv(); !ok {
		t.Fatal("subscribe ack: connection closed")
	}

	cl.send(api.Command{ID: "send", Kind: api.CmdSend, ThreadID: id, Prompt: "go"})
	waitForEvent(t, cl, func(rep api.Reply) bool {
		return rep.Kind == api.RepEvent && rep.Event != nil &&
			rep.Event.Kind == event.KindState && rep.Event.State == event.StateDone
	})
}

// Spend was folded only from a finished run's outcome, so a metered run read
// as $0.00 for its whole length: the number that justifies stopping one early
// was the number withheld until stopping no longer mattered.
func TestThreadInfo_SpendIsReportedWhileTheRunIsStillGoing(t *testing.T) {
	t.Parallel()

	local := fake.New("local",
		fake.Turn{
			ToolCalls:  []llm.ToolCall{{ID: "1", Name: "gated", Input: []byte(`{}`)}},
			StopReason: llm.StopToolUse,
			Usage:      &llm.Usage{InputTokens: 100_000, OutputTokens: 10_000},
		},
		fake.Turn{Text: []string{"done"}, StopReason: llm.StopEndTurn},
	)
	h := newHarness(t, local,
		withTool(gatedTool{echoTool: echoTool{name: "gated"}, key: "gated-key"}),
		withLoopOptions(agent.WithModels(router.Tiers[string]{Balanced: "glm-5.3"})))

	cl := dial(t, h)
	cl.hello()
	th := cl.newThread(nil)
	cl.send(api.Command{ID: "sub", Kind: api.CmdSubscribe, ThreadID: th.ID})
	cl.recvFor("sub")

	cl.send(api.Command{ID: "send", Kind: api.CmdSend, ThreadID: th.ID, Prompt: "go"})
	cl.recvFor("send")

	// The permission gate parks the run between its two turns, which is a
	// run in flight with one turn already paid for.
	pending := waitForEvent(t, cl, func(rep api.Reply) bool {
		return rep.Kind == api.RepPending && len(rep.Pending) == 1
	})

	// 100k input at $1.40 and 10k output at $4.40 per million.
	const wantFirstTurn = 0.184

	mid := listThread(t, cl, th.ID)
	if math.Abs(mid.Spend-wantFirstTurn) > 0.0001 {
		t.Fatalf("Spend = %v mid-run, want the first turn's %v", mid.Spend, wantFirstTurn)
	}

	cl.send(api.Command{
		ID: "answer", Kind: api.CmdAnswer, PromptID: pending.Pending[0].ID, Decision: permission.Allow,
	})
	cl.recvFor("answer")
	waitForEvent(t, cl, func(rep api.Reply) bool {
		return rep.Kind == api.RepEvent && rep.Event != nil && rep.Event.State == event.StateDone
	})

	// The outcome replaces the live accrual rather than adding to it.
	done := listThread(t, cl, th.ID)
	if math.Abs(done.Spend-wantFirstTurn) > 0.0001 {
		t.Fatalf("Spend = %v once the run ended, want the same %v rather than a doubled total",
			done.Spend, wantFirstTurn)
	}
}
