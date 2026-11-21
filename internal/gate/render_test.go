package gate_test

import (
	"testing"

	"github.com/kyleking/wavez/internal/gate"
)

func TestReport_Render(t *testing.T) {
	t.Parallel()

	fail := gate.Result{Check: "build", Blocking: true, Status: gate.StatusFail, Summary: "a.go:3:1: undefined: x\n"}
	advisory := gate.Result{Check: "ruff", Status: gate.StatusFail, Summary: "a.py:1:1: E501 line too long"}
	abstained := gate.Result{
		Check: "lint", Blocking: true, Status: gate.StatusAbstained, Summary: "golangci-lint not on PATH",
	}
	formatted := gate.Result{Check: "format", Blocking: true, Status: gate.StatusPass, Fixed: []string{"b.go", "a.go"}}
	passed := gate.Result{Check: "test", Blocking: true, Status: gate.StatusPass}

	tests := []struct {
		name    string
		want    string
		notes   string
		results []gate.Result
		failed  bool
	}{
		{name: "an empty report says nothing"},
		{name: "a pass says nothing", results: []gate.Result{passed}},
		{
			name: "a blocking failure leads with its check", results: []gate.Result{passed, fail}, failed: true,
			want: "FAIL build\na.go:3:1: undefined: x\n",
		},
		{
			name: "an advisory failure is marked and does not fail the report", results: []gate.Result{advisory},
			want: "ADVISORY ruff (does not block)\na.py:1:1: E501 line too long\n",
		},
		{
			name: "an abstention is a note for the person, not the agent", results: []gate.Result{abstained},
			notes: "NOT CHECKED lint: golangci-lint not on PATH\n",
		},
		{
			name: "rewritten files are named once, sorted", results: []gate.Result{formatted, formatted},
			want: "REWRITTEN by a formatter, re-read before editing: a.go b.go\n",
		},
		{
			name:    "blocking comes before advisory before rewritten",
			results: []gate.Result{formatted, abstained, advisory, fail}, failed: true,
			want: "FAIL build\na.go:3:1: undefined: x\n" +
				"ADVISORY ruff (does not block)\na.py:1:1: E501 line too long\n" +
				"REWRITTEN by a formatter, re-read before editing: a.go b.go\n",
			notes: "NOT CHECKED lint: golangci-lint not on PATH\n",
		},
		{
			name:    "a failure with no output is its header alone",
			results: []gate.Result{{Check: "x", Blocking: true, Status: gate.StatusFail}}, failed: true,
			want: "FAIL x\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			report := gate.Report{Results: tt.results}
			if got := report.Render(); got != tt.want {
				t.Errorf("Render() =\n%s\nwant\n%s", got, tt.want)
			}

			if got := report.Notes(); got != tt.notes {
				t.Errorf("Notes() =\n%s\nwant\n%s", got, tt.notes)
			}

			if report.Failed() != tt.failed {
				t.Errorf("Failed() = %v, want %v", report.Failed(), tt.failed)
			}

			if (report.Fingerprint() != "") != tt.failed {
				t.Errorf("Fingerprint() = %q with Failed() = %v", report.Fingerprint(), tt.failed)
			}
		})
	}
}

func TestReport_Fingerprint(t *testing.T) {
	t.Parallel()

	result := func(check, summary string) gate.Result {
		return gate.Result{Check: check, Blocking: true, Status: gate.StatusFail, Summary: summary}
	}
	base := gate.Report{Results: []gate.Result{
		result("build", "a.go:1:1: bad"),
		result("test", "--- FAIL: TestA (0.01s)\nFAIL\tpkg\t0.004s"),
	}}

	tests := []struct {
		name   string
		report gate.Report
		same   bool
	}{
		{
			"the same failures in another order",
			gate.Report{Results: []gate.Result{base.Results[1], base.Results[0]}},
			true,
		},
		{
			"the same failures with other timings",
			gate.Report{Results: []gate.Result{
				base.Results[0], result("test", "--- FAIL: TestA (1.20s)\nFAIL\tpkg\t1.310s"),
			}},
			true,
		},
		{
			"an added advisory, pass, and abstention",
			gate.Report{Results: append([]gate.Result{
				{Check: "ruff", Status: gate.StatusFail, Summary: "x"},
				{Check: "lint", Blocking: true, Status: gate.StatusAbstained, Summary: "y"},
				{Check: "fmt", Blocking: true, Status: gate.StatusPass},
			}, base.Results...)},
			true,
		},
		{
			"a different summary",
			gate.Report{Results: []gate.Result{result("build", "a.go:2:1: bad"), base.Results[1]}},
			false,
		},
		{
			"a different check",
			gate.Report{Results: []gate.Result{result("vet", "a.go:1:1: bad"), base.Results[1]}},
			false,
		},
		{"one failure fewer", gate.Report{Results: base.Results[:1]}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.report.Fingerprint() == base.Fingerprint(); got != tt.same {
				t.Errorf("same fingerprint = %v, want %v", got, tt.same)
			}
		})
	}
}
