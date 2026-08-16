// Package proto defines the newline-delimited JSON messages exchanged
// between wavezd and wavez over the unix socket.
package proto

// Kind values for an Event.
const (
	KindAgent      = "agent"
	KindTool       = "tool"
	KindGate       = "gate"
	KindPermission = "permission"
)

// Event is one line of daemon-to-client transcript output.
type Event struct {
	Type   string `json:"type"` // always "event"
	Thread string `json:"thread"`
	Kind   string `json:"kind"`
	Text   string `json:"text"`
	Seq    int    `json:"seq"`
}

// ListMsg answers a "list" command with the known thread names.
type ListMsg struct {
	Type    string   `json:"type"` // always "list"
	Threads []string `json:"threads"`
}

// Command is one line of client-to-daemon input.
type Command struct {
	Cmd    string `json:"cmd"` // list|subscribe|answer|send
	Thread string `json:"thread,omitempty"`
	Text   string `json:"text,omitempty"`
	Value  string `json:"value,omitempty"` // y|n, for answer
}
