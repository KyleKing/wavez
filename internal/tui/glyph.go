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
