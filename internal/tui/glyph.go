package tui

import "github.com/kyleking/wavez/internal/event"

// glyph returns the state marker for st. The ascii flag selects the
// NO_COLOR-safe fallback alphabet so meaning never depends on a Unicode
// glyph rendering.
func glyph(st event.State, ascii bool) string {
	if ascii {
		return asciiGlyphs[st]
	}

	return unicodeGlyphs[st]
}

var unicodeGlyphs = map[event.State]string{
	event.StateIdle:    "○",
	event.StateWorking: "●",
	event.StateGating:  "◐",
	event.StateNeedsIn: "▲",
	event.StateBlocked: "○",
	event.StateFailed:  "✖",
	event.StateDone:    "✔",
}

var asciiGlyphs = map[event.State]string{
	event.StateIdle:    "o",
	event.StateWorking: ">",
	event.StateGating:  "*",
	event.StateNeedsIn: "!",
	event.StateBlocked: "o",
	event.StateFailed:  "x",
	event.StateDone:    "ok",
}

// stateLabel is the state as a column reads it. Only needs_input differs
// from the wire value, because a column carries the word a person scans for
// and "waiting" is what a thread stopped for an answer is doing.
func stateLabel(st event.State) string {
	if st == event.StateNeedsIn {
		return "waiting"
	}

	return string(st)
}
