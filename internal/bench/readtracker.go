package bench

import (
	"encoding/json"

	"github.com/kyleking/wavez/internal/tool"
)

// readTracker counts reads that returned a file the model had already read
// and had not changed since. It is the cheapest available proxy for context
// the run paid for twice, and it is deliberately per file rather than per
// line range: a run that re-reads a file it just edited is doing the right
// thing, and one that re-reads a file nothing touched is not.
type readTracker struct {
	lastRead map[string]int
	lastEdit map[string]int
	step     int
}

func newReadTracker() *readTracker {
	return &readTracker{lastRead: map[string]int{}, lastEdit: map[string]int{}}
}

func (t *readTracker) edited(changes []tool.Change) {
	t.step++
	for _, c := range changes {
		t.lastEdit[c.Path] = t.step
	}
}

// repeat reports whether path was already read with no edit to it since, and
// records this read either way.
func (t *readTracker) repeat(path string) bool {
	if path == "" {
		return false
	}

	t.step++
	prev, seen := t.lastRead[path]
	t.lastRead[path] = t.step

	return seen && t.lastEdit[path] < prev
}

// inputPath pulls the path argument out of a logged tool input. The log
// stores the input as the raw JSON string the model sent, truncated, so a
// call whose input did not survive truncation simply has no path.
func inputPath(detail map[string]any) string {
	raw, ok := detail["input"].(string)
	if !ok {
		return ""
	}

	var in struct {
		Path string `json:"path"`
	}

	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		return ""
	}

	return in.Path
}

func intField(m map[string]any, key string) int {
	v, ok := m[key].(float64)
	if !ok {
		return 0
	}

	return int(v)
}

func stringField(m map[string]any, key string) string {
	v, ok := m[key].(string)
	if !ok {
		return ""
	}

	return v
}

func boolField(m map[string]any, key string) bool {
	v, ok := m[key].(bool)

	return ok && v
}
