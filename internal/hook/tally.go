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

// maxTraceLine is the longest trace line read.
const maxTraceLine = 1 << 20

// Tally counts what the hooks of one session said.
type Tally struct {
	// RouteHints is how many prompts were given a route hint.
	RouteHints int
	// RenameDenials is how many shell commands were denied as a rename by
	// text.
	RenameDenials int
	// Announcements is how many times the session was told that the gates
	// run by themselves.
	Announcements int
	// StopBlocks is how many stops the gates refused.
	StopBlocks int
	// HookTextBytes is the bytes of every text the hooks handed the agent,
	// of any kind: announcements, hints, denials, stale-file notices, and
	// gate failures. Text for the person is not in it.
	HookTextBytes int
}

// ReadTally sums a hook trace, as WithTrace writes it. A line that is not
// a trace line is skipped.
func ReadTally(trace io.Reader) (Tally, error) {
	var tally Tally

	lines := bufio.NewScanner(trace)
	lines.Buffer(nil, maxTraceLine)

	for lines.Scan() {
		var rec record
		if err := json.Unmarshal(lines.Bytes(), &rec); err != nil {
			continue
		}

		tally.HookTextBytes += rec.AgentBytes

		switch {
		case rec.Event == eventUserPromptSubmit && rec.Outcome == outcomeContext:
			tally.RouteHints++
		case rec.Event == eventPreToolUse && rec.Outcome == outcomeDeny:
			tally.RenameDenials++
		case rec.Event == eventSessionStart && rec.Outcome == outcomeContext:
			tally.Announcements++
		case rec.Event == eventStop && rec.Outcome == outcomeBlock:
			tally.StopBlocks++
		}
	}

	if err := lines.Err(); err != nil {
		return tally, fmt.Errorf("reading hook trace: %w", err)
	}

	return tally, nil
}
