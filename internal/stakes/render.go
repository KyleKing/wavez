package stakes

import (
	"strconv"
	"strings"

	"github.com/kyleking/wavez/internal/guard"
)

const (
	maxRenderedCapabilities = 3
	renderUnknown           = "unknown"
)

// Render formats s as one short line for a terminal row: an Inbox entry or
// a permission prompt. Every field renders, including an unchecked one,
// which prints as "unknown" rather than being omitted, so a signal that
// could not be computed never reads as a safe one.
func (s Score) Render() string {
	parts := []string{
		strings.ToUpper(string(s.Band)),
		"caps:" + renderCapabilities(s.Capabilities, s.CapsChecked),
		"files:" + renderEditedFiles(s.EditedFiles, s.CapsChecked),
		"guard:" + renderGuardVerdict(s.Guard),
		"revert:" + renderReversibility(s.Reversibility),
		"blast:" + renderBlastRadius(s.BlastKnown, s.BlastRadius),
	}

	return strings.Join(parts, " ")
}

func renderCapabilities(caps []Capability, checked bool) string {
	if !checked {
		return renderUnknown
	}

	if len(caps) == 0 {
		return "none"
	}

	if len(caps) <= maxRenderedCapabilities {
		return joinCapabilities(caps)
	}

	shown := joinCapabilities(caps[:maxRenderedCapabilities])

	return shown + "+" + strconv.Itoa(len(caps)-maxRenderedCapabilities)
}

func joinCapabilities(caps []Capability) string {
	strs := make([]string, len(caps))
	for i, c := range caps {
		strs[i] = string(c)
	}

	return strings.Join(strs, ",")
}

func renderEditedFiles(count int, checked bool) string {
	if !checked {
		return renderUnknown
	}

	return strconv.Itoa(count)
}

func renderGuardVerdict(v *guard.Verdict) string {
	if v == nil {
		return "n/a"
	}

	return string(*v)
}

func renderReversibility(r Reversibility) string {
	switch r {
	case Reversible:
		return "yes"
	case Irreversible:
		return "no"
	case ReversibilityUnknown:
		return renderUnknown
	default:
		return renderUnknown
	}
}

func renderBlastRadius(known bool, count int) string {
	if !known {
		return renderUnknown
	}

	return strconv.Itoa(count)
}
