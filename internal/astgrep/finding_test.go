package astgrep_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/kyleking/wavez/internal/astgrep"
)

var update = flag.Bool("update", false, "update golden ast-grep --json fixtures")

// wireFixtureMatch mirrors ast-grep's own --json match shape, used only to
// build golden fixtures without hand-typing JSON literals. Field names
// match the wire format ParseJSON decodes, not this test package's
// conventions.
//
//nolint:tagliatelle // mirrors ast-grep's own --json wire format exactly
type wireFixtureMatch struct {
	Text        string           `json:"text"`
	Replacement *string          `json:"replacement,omitempty"`
	RuleID      string           `json:"ruleId"`
	Severity    string           `json:"severity"`
	Note        string           `json:"note,omitempty"`
	Message     string           `json:"message"`
	File        string           `json:"file"`
	Lines       string           `json:"lines"`
	Range       wireFixtureRange `json:"range"`
}

type wireFixtureRange struct {
	Start wireFixturePosition `json:"start"`
	End   wireFixturePosition `json:"end"`
}

type wireFixturePosition struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

func fixp(s string) *string { return &s }

func golden(t *testing.T, name string, matches []wireFixtureMatch) []byte {
	t.Helper()

	path := filepath.Join("testdata", name+".golden")

	body, err := json.MarshalIndent(matches, "", "  ")
	if err != nil {
		t.Fatalf("marshaling test fixture: %v", err)
	}

	if *update {
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatalf("writing golden: %v", err)
		}
	}

	want, err := os.ReadFile(path) //nolint:gosec // name is a fixed test-case identifier, not attacker-controlled
	if err != nil {
		t.Fatalf("reading golden %s (run go test -update to create it): %v", path, err)
	}

	if !bytes.Equal(want, body) {
		t.Fatalf("golden %s is stale; run go test -update", path)
	}

	return want
}

func TestParseJSON(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		matches []wireFixtureMatch
		want    []astgrep.Finding
	}{
		{
			name:    "no_findings",
			matches: []wireFixtureMatch{},
			want:    []astgrep.Finding{},
		},
		{
			name: "mixed_findings",
			matches: []wireFixtureMatch{
				{
					Text:     "fmt.Println(x)",
					RuleID:   "no-fmt-println",
					Severity: "error",
					Message:  "use the project logger instead of fmt.Println",
					File:     "internal/example/main.go",
					Lines:    "\tfmt.Println(x)",
					Range: wireFixtureRange{
						Start: wireFixturePosition{Line: 12, Column: 1},
						End:   wireFixturePosition{Line: 12, Column: 16},
					},
				},
				{
					Text:        "err",
					Replacement: fixp("fmt.Errorf(\"doing thing: %w\", err)"),
					RuleID:      "wrap-errors",
					Severity:    "warning",
					Note:        "see docs/go-best-practices.md#error-handling",
					Message:     "wrap errors with %w",
					File:        "internal/example/thing.go",
					Lines:       "\treturn err",
					Range: wireFixtureRange{
						Start: wireFixturePosition{Line: 40, Column: 9},
						End:   wireFixturePosition{Line: 40, Column: 12},
					},
				},
			},
			want: []astgrep.Finding{
				{
					RuleID:   "no-fmt-println",
					Severity: "error",
					Message:  "use the project logger instead of fmt.Println",
					File:     "internal/example/main.go",
					Start:    astgrep.Position{Line: 13, Column: 2},
					End:      astgrep.Position{Line: 13, Column: 17},
				},
				{
					RuleID:   "wrap-errors",
					Severity: "warning",
					Note:     "see docs/go-best-practices.md#error-handling",
					Message:  "wrap errors with %w",
					File:     "internal/example/thing.go",
					Fix:      "fmt.Errorf(\"doing thing: %w\", err)",
					Start:    astgrep.Position{Line: 41, Column: 10},
					End:      astgrep.Position{Line: 41, Column: 13},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			data := golden(t, tt.name, tt.matches)

			got, err := astgrep.ParseJSON(data)
			if err != nil {
				t.Fatalf("ParseJSON: %v", err)
			}

			if len(got) == 0 {
				got = []astgrep.Finding{}
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ParseJSON(%s) = %+v, want %+v", tt.name, got, tt.want)
			}
		})
	}
}

func TestParseJSON_InvalidJSON(t *testing.T) {
	t.Parallel()

	if _, err := astgrep.ParseJSON([]byte("not json")); err == nil {
		t.Fatal("ParseJSON with malformed input: want error, got nil")
	}
}
