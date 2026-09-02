package tools_test

import (
	"testing"

	"github.com/kyleking/wavez/internal/tool"
	"github.com/kyleking/wavez/internal/tools"
)

// TestRiskClass pins the risk class each tool declares. The class is what the
// dispatch path reads to decide whether a call is gated, so a change here is
// a change to what a tool cannot skip.
func TestRiskClass(t *testing.T) {
	t.Parallel()

	sweep := tools.NewSweep(".", nil, nil)
	cases := []struct {
		name string
		t    tool.Tool
		want tool.RiskClass
	}{
		{"list", tools.NewList("."), tool.RiskRead},
		{"read", tools.NewRead(".", nil), tool.RiskRead},
		{"search", tools.NewSearch(nil, "."), tool.RiskRead},
		{"context", tools.NewContext(nil), tool.RiskRead},
		{"sweep", sweep, tool.RiskRead},
		{"str_replace", tools.NewStrReplace(".", nil), tool.RiskWriteLocal},
		{"write", tools.NewWrite(".", nil), tool.RiskWriteLocal},
		{"delete", tools.NewDelete(".", nil, nil, nil), tool.RiskWriteLocal},
		{"rename", tools.NewRename(".", nil, nil, nil), tool.RiskWriteLocal},
		{"move", tools.NewMove(".", nil, nil), tool.RiskWriteLocal},
		{"declare", tools.NewDeclare(".", nil, nil), tool.RiskWriteLocal},
		{"undo", tools.NewUndo(".", nil), tool.RiskWriteLocal},
		{"shell", tools.NewShell(".", "", "", nil), tool.RiskExec},
		{"pty", tools.NewPTY(".", "", "", nil), tool.RiskExec},
		{"question", tools.NewQuestion(nil), tool.RiskExternal},
		{"hypothesis", tools.NewHypothesis(nil), tool.RiskRead},
		{"web_fetch", mustWebFetch(t), tool.RiskEgress},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.t.Risk(); got != tc.want {
				t.Errorf("%s Risk() = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func mustWebFetch(t *testing.T) *tools.WebFetch {
	t.Helper()

	_, fetch := tools.NewWeb("", "t1", nil)

	return fetch
}
