package astgrep

import (
	"encoding/json"
	"fmt"
)

// Position is a zero-width point in a file, both fields 1-based.
type Position struct {
	Line   int
	Column int
}

// Finding is one ast-grep rule match, parsed from `ast-grep scan --json`.
type Finding struct {
	RuleID   string
	Severity string
	Message  string
	Note     string
	File     string
	Fix      string
	Start    Position
	End      Position
}

// wireMatch mirrors the object shape ast-grep's own --json output defines
// (https://ast-grep.github.io/guide/tools/json.html), so its field names
// are fixed by that wire format rather than this package's conventions.
// Fields ast-grep emits that this package does not use (text, lines,
// metaVariables, byteOffset) are left undeclared and dropped on decode.
//
//nolint:tagliatelle // field names match ast-grep's own --json wire format exactly
type wireMatch struct {
	Replacement *string   `json:"replacement"`
	RuleID      string    `json:"ruleId"`
	Severity    string    `json:"severity"`
	Note        string    `json:"note"`
	Message     string    `json:"message"`
	File        string    `json:"file"`
	Range       wireRange `json:"range"`
}

type wireRange struct {
	Start wirePosition `json:"start"`
	End   wirePosition `json:"end"`
}

// wirePosition is 0-based line and column, per ast-grep's LSP-compatible
// coordinates; ParseJSON converts to Position's 1-based coordinates.
type wirePosition struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

// ParseJSON parses one `ast-grep scan --json` output array into Findings.
func ParseJSON(data []byte) ([]Finding, error) {
	var matches []wireMatch
	if err := json.Unmarshal(data, &matches); err != nil {
		return nil, fmt.Errorf("parsing ast-grep --json output: %w", err)
	}

	findings := make([]Finding, 0, len(matches))
	for _, m := range matches {
		fix := ""
		if m.Replacement != nil {
			fix = *m.Replacement
		}

		findings = append(findings, Finding{
			RuleID:   m.RuleID,
			Severity: m.Severity,
			Message:  m.Message,
			Note:     m.Note,
			File:     m.File,
			Fix:      fix,
			Start:    Position{Line: m.Range.Start.Line + 1, Column: m.Range.Start.Column + 1},
			End:      Position{Line: m.Range.End.Line + 1, Column: m.Range.End.Column + 1},
		})
	}

	return findings, nil
}
