package lsp

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// Severity is an LSP DiagnosticSeverity. The wire values are fixed by the
// specification, so they are declared rather than derived.
type Severity int

// Severities a server may report, most severe first, matching the LSP
// DiagnosticSeverity enumeration.
const (
	SeverityError       Severity = 1
	SeverityWarning     Severity = 2
	SeverityInformation Severity = 3
	SeverityHint        Severity = 4
)

// Diagnostic is one server finding, flattened to what a caller acts on.
// Line and Character are 1-based, unlike the protocol's 0-based positions.
type Diagnostic struct {
	Path      string
	Source    string
	Code      string
	Message   string
	Line      int
	Character int
	Severity  Severity
}

// String renders a diagnostic as "path:line:col: message [source]", the shape
// a compiler uses and a model already reads.
func (d Diagnostic) String() string {
	out := fmt.Sprintf("%s:%d:%d: %s", d.Path, d.Line, d.Character, d.Message)
	if d.Source != "" {
		out += fmt.Sprintf(" [%s]", d.Source)
	}

	return out
}

// publication is one textDocument/publishDiagnostics notification. Version is
// a pointer because the protocol makes it optional: a server that omits it is
// saying it does not track document versions, which is not the same as
// reporting version zero.
type publication struct {
	Version     *int32           `json:"version"`
	URI         string           `json:"uri"`
	Diagnostics []wireDiagnostic `json:"diagnostics"`
}

type wireDiagnostic struct {
	Code     any       `json:"code"`
	Source   string    `json:"source"`
	Message  string    `json:"message"`
	Range    wireRange `json:"range"`
	Severity int       `json:"severity"`
}

type wireRange struct {
	Start wirePosition `json:"start"`
}

type wirePosition struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

func decodePublication(params json.RawMessage) (publication, error) {
	var p publication
	if err := json.Unmarshal(params, &p); err != nil {
		return publication{}, fmt.Errorf("decoding publishDiagnostics: %w", err)
	}

	return p, nil
}

// diagnostics converts a publication's findings for the file at path.
// A diagnostic carrying no severity is treated as an error: the protocol
// leaves the choice to the client, and dropping a finding a server declined
// to rank is the one outcome a gate cannot afford.
func (p publication) diagnostics(path string) []Diagnostic {
	out := make([]Diagnostic, 0, len(p.Diagnostics))

	for _, d := range p.Diagnostics {
		severity := Severity(d.Severity)
		if d.Severity == 0 {
			severity = SeverityError
		}

		out = append(out, Diagnostic{
			Path:      path,
			Source:    d.Source,
			Code:      codeString(d.Code),
			Message:   d.Message,
			Line:      d.Range.Start.Line + 1,
			Character: d.Range.Start.Character + 1,
			Severity:  severity,
		})
	}

	return out
}

func codeString(code any) string {
	switch v := code.(type) {
	case nil:
		return ""
	case string:
		return v
	case float64:
		return strconv.FormatInt(int64(v), 10)
	default:
		return fmt.Sprint(v)
	}
}
