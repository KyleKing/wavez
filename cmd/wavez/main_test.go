package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kyleking/wavez/internal/agent"
	"github.com/kyleking/wavez/internal/api"
	"github.com/kyleking/wavez/internal/config"
	"github.com/kyleking/wavez/internal/event"
	"github.com/kyleking/wavez/internal/permission"
)

// linkifyText is the pure function behind `-p` text mode's markdown-link
// rendering; cmd/wavez has no CLI-level test harness, so this exercises it
// directly rather than shelling out to the built binary.
func TestLinkifyText(t *testing.T) {
	repo := []config.LinkPattern{
		{Pattern: `#(\d+)`, URL: "https://github.com/kyleking/wavez/pull/$1"},
	}

	t.Run("matched identifier becomes a markdown link", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("XDG_CONFIG_HOME", "")

		got := linkifyText(repo, "fixes #42 today")
		want := "fixes [#42](https://github.com/kyleking/wavez/pull/42) today"
		if got != want {
			t.Errorf("linkifyText() = %q, want %q", got, want)
		}
	})

	t.Run("no match leaves text unchanged", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("XDG_CONFIG_HOME", "")

		got := linkifyText(repo, "no identifiers here")
		if got != "no identifiers here" {
			t.Errorf("linkifyText() = %q, want the text unchanged", got)
		}
	})

	t.Run("invalid repo pattern leaves text unchanged", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		t.Setenv("XDG_CONFIG_HOME", "")

		bad := []config.LinkPattern{{Pattern: `#(\d+`, URL: "https://example.com/$1"}}

		got := linkifyText(bad, "fixes #42 today")
		if got != "fixes #42 today" {
			t.Errorf("linkifyText() = %q, want the text unchanged on a bad pattern", got)
		}
	})
}

// TestWritePreamble covers what the audit is read for: the biggest section
// first, and a per-kind rollup that still sums to the total after sorting
// reorders the rows.
func TestWritePreamble(t *testing.T) {
	t.Parallel()

	var b strings.Builder

	sections := []section{
		{Name: "system rules", Kind: "system", Bytes: 100},
		{Name: "read (schema)", Kind: "schema", Bytes: 400},
		{Name: "read (text)", Kind: "tool", Bytes: 200},
	}

	// The fast tier is shown fewer tools, so its cost is reported against
	// its own window rather than folded into one number.
	fastOnly := withoutTools(sections, []string{"read"})

	if err := writePreamble(&b, sections, fastOnly, 8192); err != nil {
		t.Fatalf("writePreamble: %v", err)
	}

	out := b.String()
	if !strings.Contains(out, "total") || !strings.Contains(out, "700") {
		t.Errorf("want a total of 700 bytes, got:\n%s", out)
	}

	first := strings.Index(out, "read (schema)")
	if first == -1 || first > strings.Index(out, "system rules") {
		t.Errorf("want the largest section first, got:\n%s", out)
	}

	if !strings.Contains(out, "fast ") || !strings.Contains(out, "hosted ") {
		t.Errorf("want a line per tier, got:\n%s", out)
	}

	if len(fastOnly) != 1 {
		t.Errorf("withoutTools kept %d sections, want only the ones the fast tier is shown", len(fastOnly))
	}

	if !strings.Contains(out, "57.1%") {
		t.Errorf("want the schema kind at 400/700, got:\n%s", out)
	}
}

// The fixed prefix is 42% of what a fast turn can use and only shrinks when
// somebody remembers to look. A ceiling that fails the build makes every
// new tool's cost a decision rather than a discovery.
func TestPreambleBudget(t *testing.T) {
	t.Parallel()

	sections := []section{{Bytes: 4000}, {Bytes: 400}}

	if err := withinBudget(sections, 0); err != nil {
		t.Errorf("withinBudget with no ceiling = %v, want it to abstain", err)
	}

	if err := withinBudget(sections, 1100); err != nil {
		t.Errorf("withinBudget under the ceiling = %v, want nil", err)
	}

	err := withinBudget(sections, 1000)
	if !errors.Is(err, errPreambleOverBudget) {
		t.Fatalf("withinBudget over the ceiling = %v, want %v", err, errPreambleOverBudget)
	}

	if !strings.Contains(err.Error(), "1100") {
		t.Errorf("error = %q, want it to say what the prefix actually costs", err)
	}
}

// The preamble's two costs are different kinds of thing and the report has
// to keep them apart: structure is the grammar a fast turn decodes under,
// and prose is teaching. The halves are derived rather than counted so the
// rollup still totals the bytes the model is actually sent.
func TestSplitSchemaAccountsForEveryByte(t *testing.T) {
	t.Parallel()

	raw := json.RawMessage(`{"type":"object","properties":` +
		`{"path":{"type":"string","description":"where the file is"}},"required":["path"]}`)

	cost, err := splitSchema(raw)
	if err != nil {
		t.Fatalf("splitSchema: %v", err)
	}

	if cost.Prose+cost.Structure != len(raw) {
		t.Errorf("prose %d + structure %d = %d, want the schema's %d bytes",
			cost.Prose, cost.Structure, cost.Prose+cost.Structure, len(raw))
	}

	if cost.Prose <= len("where the file is") {
		t.Errorf("prose = %d, want it to carry the description's key and quoting too", cost.Prose)
	}
}

// writePending is `-inbox`'s whole output, so what a parked fleet looks like
// is checked here rather than against a live daemon.
func TestWritePending(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	t.Run("empty fleet names the empty state", func(t *testing.T) {
		t.Parallel()

		var b bytes.Buffer
		if err := writePending(&b, nil, now); err != nil {
			t.Fatalf("writePending: %v", err)
		}
		if b.String() != "no pending prompts\n" {
			t.Errorf("output = %q", b.String())
		}
	})

	t.Run("one line per prompt, question and permission alike", func(t *testing.T) {
		t.Parallel()

		var b bytes.Buffer
		pending := []api.PendingInfo{
			{
				ID: "p1", Thread: "fix-login", Question: true,
				Detail: "which  test\ndatabase?", Asked: now.Add(-90 * time.Second),
			},
			{
				ID: "p2", ThreadID: "abc123", Tool: "shell",
				Detail: "rm -rf build", Reason: "deletes a path", Asked: now.Add(-2 * time.Second),
			},
		}
		if err := writePending(&b, pending, now); err != nil {
			t.Fatalf("writePending: %v", err)
		}

		out := b.String()
		for _, want := range []string{
			"p1  fix-login     question   wait 1m30s   which test database?",
			"p2  abc123        permission  wait 2s      deletes a path - rm -rf build",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("output missing %q:\n%s", want, out)
			}
		}
	})
}

// parseDecision is the whole permission half of `-answer`: a decision read as
// a person would type it, with a typed error for anything else.
func TestParseDecision(t *testing.T) {
	t.Parallel()

	tests := []struct {
		text string
		want permission.Decision
	}{
		{text: "allow", want: permission.Allow},
		{text: "YES", want: permission.Allow},
		{text: "deny", want: permission.Deny},
		{text: "No", want: permission.Deny},
		{text: "n", want: permission.Deny},
		{text: "always", want: permission.AllowAlways},
		{text: "allow_always", want: permission.AllowAlways},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			t.Parallel()

			got, err := parseDecision(tt.text)
			if err != nil || got != tt.want {
				t.Errorf("parseDecision(%q) = %v, %v; want %v", tt.text, got, err, tt.want)
			}
		})
	}

	_, err := parseDecision("maybe")
	if !errors.Is(err, errUnknownDecision) {
		t.Errorf("parseDecision(maybe) error = %v, want errUnknownDecision", err)
	}
}

// which only the log used to hold. One lane rebutted a correct objection
// twice with a claim its own file contradicted, and the terminal showed the
// rebuttal and nothing else.
func TestReportStandingObjection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		verdict agent.Verdict
		want    string
	}{
		{
			name:    "an objection the run completed with is printed",
			verdict: agent.Verdict{Result: agent.ReviewObjection, Note: "the Usage section has no example"},
			want:    "review still objects: the Usage section has no example\n",
		},
		{name: "a clean review says nothing", verdict: agent.Verdict{Result: agent.ReviewOK}},
		{name: "an objection with no note says nothing", verdict: agent.Verdict{Result: agent.ReviewObjection}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var b bytes.Buffer

			reportStandingObjection(&b, tt.verdict)

			if b.String() != tt.want {
				t.Errorf("output = %q, want %q", b.String(), tt.want)
			}
		})
	}
}

// -inbox answers what is blocked and -threads answers what exists, which was
// otherwise readable only from the TUI or by parsing a thread's event log.
// Spend and the share of the window a thread fills are the two numbers that
// decide what leaving it parked costs, since past the provider's cache
// lifetime the whole prefix is re-read.
func TestWriteThreads(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 4, 22, 0, 0, 0, time.UTC)

	var buf bytes.Buffer

	err := writeThreads(&buf, []api.ThreadInfo{
		{
			ID: "old0000000000000", Name: "an older thread", State: event.StateDone,
			Step: "done", LastEvent: now.Add(-time.Hour),
		},
		{
			ID: "new0000000000000", Name: "a name long enough to be cut short here",
			State: event.StateWorking, Step: "editing  traverse.py", LastEvent: now.Add(-30 * time.Second),
			Context: 42, Window: 100,
		},
	}, now)
	if err != nil {
		t.Fatalf("writeThreads: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2:\n%s", len(lines), buf.String())
	}

	if !strings.HasPrefix(lines[0], "new0000000000000") {
		t.Errorf("newest activity is not first:\n%s", buf.String())
	}

	if !strings.Contains(lines[0], "$0.00") || !strings.Contains(lines[0], "42%") {
		t.Errorf("spend and context share are missing, which is what a parked thread costs:\n%s", buf.String())
	}

	if !strings.Contains(lines[0], "editing traverse.py") {
		t.Errorf("the step is not collapsed onto one line:\n%s", buf.String())
	}

	if strings.Contains(lines[0], "be cut short here") {
		t.Errorf("a long name was not truncated:\n%s", buf.String())
	}
}
