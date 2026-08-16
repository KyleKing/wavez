package event

import "time"

const (
	KindWrite        = "write"
	KindSessionStart = "session_start"
	KindSessionEnd   = "session_end"
	KindCommit       = "commit"
	KindClaim        = "claim"
	KindRelease      = "release"
	KindWarn         = "warn"
)

const (
	OwnerAgent = "agent"
	OwnerHuman = "human"
)

type Event struct {
	TS      time.Time `json:"ts"`
	Kind    string    `json:"kind"`
	Owner   string    `json:"owner"`
	Session string    `json:"session,omitempty"`
	Agent   string    `json:"agent,omitempty"`
	Root    string    `json:"root"`
	Dir     string    `json:"dir,omitempty"`
	Path    string    `json:"path,omitempty"`
	Tool    string    `json:"tool,omitempty"`
	Peer    string    `json:"peer,omitempty"`
	Note    string    `json:"note,omitempty"`
}

// Actor identifies a writer. Subagent hooks carry the parent's session id, so the
// agent id is required to tell concurrent subagents apart.
func Actor(session, agent string) string {
	if agent == "" {
		return session
	}
	return session + "/" + agent
}

func (e Event) Actor() string { return Actor(e.Session, e.Agent) }

func Short(actor string) string {
	if len(actor) <= 8 {
		return actor
	}
	return actor[:8]
}
