// Package decision defines a quiz-worthy decision point extracted from a
// session: something the agent did, and why (when known).
package decision

// Source identifies how a Decision's rationale was obtained.
type Source string

const (
	// SourceStructural means only structural facts were available (tool
	// name, files touched); no rationale text was found in the transcript.
	SourceStructural Source = "structural"
	// SourceTranscript means the rationale was taken from the agent's own
	// text in the transcript, adjacent to the tool call.
	SourceTranscript Source = "transcript"
	// SourceLLM means the rationale was synthesized by a second LLM pass
	// because the transcript did not state it explicitly.
	SourceLLM Source = "llm"
)

// Decision is one quiz-worthy thing the agent did during a session.
type Decision struct {
	ID        string
	SessionID string
	Summary   string
	Rationale string
	Source    Source
	Files     []string
	ToolNames []string
}
