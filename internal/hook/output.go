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

// The outcomes a trace line can carry.
const (
	outcomeSilent   = "silent"
	outcomeContext  = "context"
	outcomeDeny     = "deny"
	outcomeBlock    = "block"
	outcomeReleased = "released"
	// A notice is text for the person and nothing for the agent.
	outcomeNotice = "notice"
	outcomeError  = "error"
)

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
	// released marks a stop that was let through with the gates still
	// failing.
	released bool
}

// specific carries what one event defines for itself. AdditionalContext is
// text for the model that is not an error: next to the tool result on
// PostToolUse, beside the prompt on UserPromptSubmit, ahead of the first
// prompt on SessionStart, and as feedback that continues the turn once on
// Stop. PermissionDecision "deny" on a PreToolUse event stops the tool call
// and hands the model PermissionReason in its place.
type specific struct {
	EventName          string
	AdditionalContext  string
	PermissionDecision string
	PermissionReason   string
}

// permissionDeny is the PreToolUse decision that stops a tool call.
const permissionDeny = "deny"

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
		own := map[string]string{"hookEventName": o.Specific.EventName}

		if o.Specific.PermissionDecision != "" {
			own["permissionDecision"] = o.Specific.PermissionDecision
			own["permissionDecisionReason"] = o.Specific.PermissionReason
		} else {
			own["additionalContext"] = o.Specific.AdditionalContext
		}

		fields["hookSpecificOutput"] = own
	}

	return fields
}

// agentText is everything in the output the model reads. Its length is what
// a hook costs the session, on this turn and on every later one.
func (o output) agentText() string {
	text := o.Reason

	if o.Specific != nil {
		text += o.Specific.AdditionalContext + o.Specific.PermissionReason
	}

	return text
}

func contextFor(eventName, text string) output {
	return output{Specific: &specific{EventName: eventName, AdditionalContext: clip(text)}}
}

// deny refuses a tool call and tells the model why.
func deny(reason string) output {
	return output{Specific: &specific{
		EventName: eventPreToolUse, PermissionDecision: permissionDeny, PermissionReason: clip(reason),
	}}
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
		return outcomeBlock
	case o.released:
		return outcomeReleased
	case o.Specific != nil && o.Specific.PermissionDecision != "":
		return outcomeDeny
	case o.Specific != nil:
		return outcomeContext
	case o.SystemMessage != "":
		return outcomeNotice
	default:
		return outcomeSilent
	}
}
