package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/config"
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

	if err := writePreamble(&b, sections); err != nil {
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
