package astgrep_test

import (
	"context"
	"errors"
	"testing"

	"github.com/kyleking/wavez/internal/astgrep"
)

var (
	errFakeLookPath = errors.New("not found")
	errFakeCommand  = errors.New("boom")
)

func alwaysAbsent(string) (string, error) {
	return "", errFakeLookPath
}

func TestRunner_Scan_BinaryAbsent(t *testing.T) {
	t.Parallel()

	r := astgrep.NewRunner(astgrep.WithLookPath(alwaysAbsent))
	rules := []astgrep.RuleFile{{ID: "no-fmt-println", Path: "rule.yml"}}

	report, err := r.Scan(context.Background(), t.TempDir(), rules)
	if !errors.Is(err, astgrep.ErrUnavailable) {
		t.Fatalf("Scan error = %v, want wrapping ErrUnavailable", err)
	}

	if report.Available {
		t.Errorf("Report.Available = true with no binary on PATH, want false")
	}

	if report.InstallHint == "" {
		t.Errorf("Report.InstallHint is empty, want an install hint")
	}

	if report.Findings != nil {
		t.Errorf("Report.Findings = %+v, want nil when unavailable", report.Findings)
	}
}

func TestRunner_Resolve_TriesFallbackBinary(t *testing.T) {
	t.Parallel()

	r := astgrep.NewRunner(astgrep.WithLookPath(func(name string) (string, error) {
		if name == "sg" {
			return "/usr/local/bin/sg", nil
		}

		return "", errFakeLookPath
	}))

	avail := r.Resolve()
	if !avail.Available || avail.Binary != "/usr/local/bin/sg" {
		t.Errorf("Resolve() = %+v, want the sg fallback resolved", avail)
	}
}

func TestRunner_Scan_AggregatesFindingsAcrossRuleFiles(t *testing.T) {
	t.Parallel()

	calls := map[string]int{}

	fakeCmd := func(_ context.Context, _ string, args []string, _ string) ([]byte, error) {
		ruleFile := ""

		for i, a := range args {
			if a == "--rule" && i+1 < len(args) {
				ruleFile = args[i+1]
			}
		}

		calls[ruleFile]++

		switch ruleFile {
		case "a.yml":
			return []byte(`[{"ruleId":"rule-a","severity":"error","message":"a","file":"x.go",` +
				`"range":{"start":{"line":0,"column":0},"end":{"line":0,"column":1}}}]`), nil
		case "b.yml":
			return []byte(`[]`), nil
		default:
			t.Fatalf("unexpected rule file %q", ruleFile)

			return nil, nil
		}
	}

	r := astgrep.NewRunner(
		astgrep.WithLookPath(func(string) (string, error) { return "/usr/local/bin/ast-grep", nil }),
		astgrep.WithCommand(fakeCmd),
	)

	report, err := r.Scan(context.Background(), t.TempDir(), []astgrep.RuleFile{
		{ID: "rule-a", Path: "a.yml"},
		{ID: "rule-b", Path: "b.yml"},
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	if !report.Available {
		t.Error("Report.Available = false, want true")
	}

	if len(report.Findings) != 1 || report.Findings[0].RuleID != "rule-a" {
		t.Errorf("Report.Findings = %+v, want one finding from rule-a", report.Findings)
	}

	if calls["a.yml"] != 1 || calls["b.yml"] != 1 {
		t.Errorf("calls = %+v, want exactly one invocation per rule file", calls)
	}
}

func TestRunner_Scan_CommandFailurePropagates(t *testing.T) {
	t.Parallel()

	r := astgrep.NewRunner(
		astgrep.WithLookPath(func(string) (string, error) { return "/usr/local/bin/ast-grep", nil }),
		astgrep.WithCommand(func(context.Context, string, []string, string) ([]byte, error) {
			return nil, errFakeCommand
		}),
	)

	_, err := r.Scan(context.Background(), t.TempDir(), []astgrep.RuleFile{{ID: "rule-a", Path: "a.yml"}})
	if err == nil {
		t.Fatal("Scan with a failing command: want error, got nil")
	}
}
