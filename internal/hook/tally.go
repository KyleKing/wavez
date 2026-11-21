package hook

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// TraceEnv names the environment variable holding a file `wavez hook`
// appends one JSON line per event to.
const TraceEnv = "WAVEZ_HOOK_LOG"

// The outcomes a trace line can carry that Tally counts.
const (
	outcomeContext = "context"
	outcomeDeny    = "deny"
)

// maxTraceLine is the longest trace line read.
const maxTraceLine = 1 << 20

// Tally counts what the hooks of one session said beyond the gates.
type Tally struct {
	// RouteHints is how many prompts were given a route hint.
	RouteHints int
	// RenameDenials is how many shell commands were denied as a rename by
	// text.
	RenameDenials int
}

// ReadTally counts the route hints and rename denials in a hook trace, as
// WithTrace writes it. A line that is not a trace line is skipped.
func ReadTally(trace io.Reader) (Tally, error) {
	var tally Tally

	lines := bufio.NewScanner(trace)
	lines.Buffer(nil, maxTraceLine)

	for lines.Scan() {
		var rec record
		if err := json.Unmarshal(lines.Bytes(), &rec); err != nil {
			continue
		}

		switch {
		case rec.Event == eventUserPromptSubmit && rec.Outcome == outcomeContext:
			tally.RouteHints++
		case rec.Event == eventPreToolUse && rec.Outcome == outcomeDeny:
			tally.RenameDenials++
		}
	}

	if err := lines.Err(); err != nil {
		return tally, fmt.Errorf("reading hook trace: %w", err)
	}

	return tally, nil
}
