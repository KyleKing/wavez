package gate

import (
	"testing"

	"github.com/kyleking/wavez/internal/tool"
)

// TestRunScopeKeepsEachWriterSeparate is the defect a single current run had:
// one agent.Loop serves every thread, so a lane starting beside a lane still
// working used to take the identity out from under it, and that lane's next
// lint re-recorded its baseline and read its own new findings as inherited.
func TestRunScopeKeepsEachWriterSeparate(t *testing.T) {
	t.Parallel()

	s := NewRunScope()

	first := s.Begin("thread-a")
	if s.Begin("thread-b") == first {
		t.Fatal("two writers were handed the same run identity")
	}

	if got := s.Current("thread-a"); got != first {
		t.Errorf("thread-a's run = %q after thread-b began, want %q", got, first)
	}

	if second := s.Begin("thread-a"); second == first {
		t.Error("a writer's second run reused its first run's identity")
	}
}

// TestRunScopeWithoutAWriter covers the two callers that have no identity to
// give: a nil scope, and a batch whose writer soleWriter could not name.
func TestRunScopeWithoutAWriter(t *testing.T) {
	t.Parallel()

	var nilScope *RunScope
	if nilScope.Begin("thread-a") != "" || nilScope.Current("thread-a") != "" {
		t.Error("a nil scope handed out a run identity")
	}

	s := NewRunScope()
	if s.Begin("") != "" || s.Current("") != "" {
		t.Error("an unnamed writer was handed a run identity")
	}
}

// TestSoleWriter covers what a gate batch is told about who produced it. A
// batch mixing two threads has no single starting point, so it is handed no
// identity and every gate reading one falls back to what it did before.
func TestSoleWriter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		want    string
		changes []tool.Change
	}{
		{name: "no changes"},
		{name: "unstamped changes", changes: []tool.Change{{Path: "a.go"}}},
		{
			name:    "one writer",
			changes: []tool.Change{{Path: "a.go", Writer: "t1"}, {Path: "b.go", Writer: "t1"}},
			want:    "t1",
		},
		{
			name:    "one writer beside an unstamped change",
			changes: []tool.Change{{Path: "a.go"}, {Path: "b.go", Writer: "t1"}},
			want:    "t1",
		},
		{
			name:    "two writers",
			changes: []tool.Change{{Path: "a.go", Writer: "t1"}, {Path: "b.go", Writer: "t2"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := soleWriter(tt.changes); got != tt.want {
				t.Errorf("soleWriter = %q, want %q", got, tt.want)
			}
		})
	}
}
