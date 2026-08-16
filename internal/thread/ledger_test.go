package thread_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/thread"
	"github.com/kyleking/wavez/internal/tool"
)

func TestLedgerDerivesFromEvents(t *testing.T) {
	t.Parallel()

	events := []event.Event{
		{Kind: event.KindUser, Text: "do the thing"},
		{Kind: event.KindAgent},
		{Kind: event.KindTool, Changes: []tool.Change{{Path: "a.go"}, {Path: "b.go"}}},
		{Kind: event.KindGate},
		{Kind: event.KindAgent},
		{Kind: event.KindTool, Changes: []tool.Change{{Path: "a.go"}}},
		{Kind: event.KindGate},
	}

	got := thread.Ledger(events)

	want := thread.LedgerSummary{Turns: 2, GatesRun: 2, FilesChanged: []string{"a.go", "b.go"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Ledger = %+v, want %+v", got, want)
	}
}

func TestWriteLedgerAppendsKindLedger(t *testing.T) {
	t.Parallel()

	th := open(t)
	ctx := context.Background()

	if err := th.AppendUser(ctx, "hi"); err != nil {
		t.Fatalf("AppendUser: %v", err)
	}
	th.BeginTurn()
	if err := th.AppendToolResult(ctx, "1", "edit", nil, tool.Result{
		Content: "ok",
		Changes: []tool.Change{{Path: "x.go"}},
	}); err != nil {
		t.Fatalf("AppendToolResult: %v", err)
	}

	summary, err := th.WriteLedger(ctx)
	if err != nil {
		t.Fatalf("WriteLedger: %v", err)
	}
	if len(summary.FilesChanged) != 1 || summary.FilesChanged[0] != "x.go" {
		t.Errorf("FilesChanged = %v, want [x.go]", summary.FilesChanged)
	}

	events, err := th.Log().Since(0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}

	last := events[len(events)-1]
	if last.Kind != event.KindLedger {
		t.Fatalf("last event kind = %q, want ledger", last.Kind)
	}
	if last.Text == "" {
		t.Error("ledger event Text is empty")
	}
}
