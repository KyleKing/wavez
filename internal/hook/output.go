package hook

import (
	"encoding/json"
	"fmt"
	"io"
)

// contextCap keeps a message under the 10,000 characters Claude Code allows
// a hook's additionalContext or systemMessage before it swaps the text for
// a file path and a preview.
const contextCap = 9500

// output is what a hook answers with. It is printed as one JSON object on
// stdout with exit status 0, and Claude Code reads it only when it is the
// whole of stdout.
type output struct {
	Specific *specific
	// Decision "block" on a Stop event keeps the agent working and hands
	// it Reason.
	Decision string
	Reason   string
	// SystemMessage is shown to the user and never reaches the model.
	SystemMessage string
}

// specific carries text for the model that is not an error: next to the
// tool result on PostToolUse, and as feedback that continues the turn once
// on Stop.
type specific struct {
	EventName         string
	AdditionalContext string
}

// wire lays the output out under the camelCase keys Claude Code defines.
func (o output) wire() map[string]any {
	fields := map[string]any{}

	if o.Decision != "" {
		fields["decision"] = o.Decision
		fields["reason"] = o.Reason
	}

	if o.SystemMessage != "" {
		fields["systemMessage"] = o.SystemMessage
	}

	if o.Specific != nil {
		fields["hookSpecificOutput"] = map[string]string{
			"hookEventName":     o.Specific.EventName,
			"additionalContext": o.Specific.AdditionalContext,
		}
	}

	return fields
}

func contextFor(eventName, text string) output {
	return output{Specific: &specific{EventName: eventName, AdditionalContext: clip(text)}}
}

func (o output) write(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	if err := enc.Encode(o.wire()); err != nil {
		return fmt.Errorf("writing hook output: %w", err)
	}

	return nil
}

// clip shortens text to the cap, cutting on a rune boundary.
func clip(text string) string {
	runes := []rune(text)
	if len(runes) <= contextCap {
		return text
	}

	return string(runes[:contextCap]) + "\n[truncated by wavez]"
}

func (o output) empty() bool {
	return o.Decision == "" && o.SystemMessage == "" && o.Specific == nil
}

func (o output) outcome() string {
	switch {
	case o.Decision != "":
		return "block"
	case o.SystemMessage != "":
		return "released"
	case o.Specific != nil:
		return "context"
	default:
		return "silent"
	}
}
