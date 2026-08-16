package stakes

import (
	"regexp"
	"sort"
	"strings"
)

// Capability names a dangerous kind of operation capabilityDelta can detect
// as newly introduced between an edit's before and after content.
type Capability string

const (
	// CapabilitySubprocess is a newly introduced ability to spawn a process.
	CapabilitySubprocess Capability = "subprocess"
	// CapabilityNetwork is a newly introduced outbound network call.
	CapabilityNetwork Capability = "network"
	// CapabilitySQL is a newly introduced raw SQL construction or execution.
	CapabilitySQL Capability = "sql"
	// CapabilityAuth is a newly introduced auth or permission-check call.
	CapabilityAuth Capability = "auth"
	// CapabilityImport is a newly introduced third-party import.
	CapabilityImport Capability = "import"
)

type capabilityPattern struct {
	pattern    *regexp.Regexp
	capability Capability
}

// capabilityPatterns is intentionally syntactic, not semantic: it catches a
// capability visible to a regex over the literal text, not one reached
// through aliasing, string-built dispatch, or a dependency bump. See
// _ai_/notes/is-it-risky-deterministically.md for that tradeoff argued in
// full; it is a real risk separator even so.
var capabilityPatterns = []capabilityPattern{
	{
		capability: CapabilitySubprocess,
		pattern: regexp.MustCompile(`(?i)(os/exec|exec\.Command|subprocess\.(run|popen|call|check_call|check_output)|` +
			`os\.(system|popen)\s*\(|child_process|execa\()`),
	},
	{
		capability: CapabilityNetwork,
		pattern: regexp.MustCompile(`(?i)(net/http|http\.(get|post|newrequest)\(|net\.dial|` +
			`requests\.(get|post|put|delete|patch)\(|urllib\.request|httpx\.|fetch\(|axios\.|net\.connect)`),
	},
	{
		capability: CapabilitySQL,
		pattern: regexp.MustCompile(`(?i)(database/sql|sql\.open\(|\.raw\(|cursor\.execute\(|` +
			`select\s+.+\s+from\s|insert\s+into\s|update\s+.+\s+set\s|delete\s+from\s)`),
	},
	{
		capability: CapabilityAuth,
		pattern: regexp.MustCompile(`(?i)(is_?authorized|require_?auth|login_required|permission_classes|` +
			`authmiddleware|checkpermission|authorize\()`),
	},
}

var importPatterns = []*regexp.Regexp{
	regexp.MustCompile(`^\s*\w*\s*"([^"]+)"\s*$`),
	regexp.MustCompile(`^\s*import\s+(?:\w+\s+)?"([^"]+)"`),
	regexp.MustCompile(`^\s*(?:import|from)\s+([\w.]+)`),
	regexp.MustCompile(`(?:from|import)\s+['"]([^'"]+)['"]`),
	regexp.MustCompile(`require\(\s*['"]([^'"]+)['"]\s*\)`),
}

// capabilityDelta reports every Capability newly present across edits'
// After content that was not already present in the matching Before
// content, plus whether the check ran at all: an empty edits slice means
// the signal could not be computed, distinct from a computed empty result.
func capabilityDelta(edits []Edit) ([]Capability, bool) {
	if len(edits) == 0 {
		return nil, false
	}

	seen := make(map[Capability]bool)

	for _, e := range edits {
		for _, cp := range capabilityPatterns {
			if cp.pattern.MatchString(e.After) && !cp.pattern.MatchString(e.Before) {
				seen[cp.capability] = true
			}
		}

		if len(importDelta(e.Before, e.After)) > 0 {
			seen[CapabilityImport] = true
		}
	}

	out := make([]Capability, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}

	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })

	return out, true
}

// importDelta returns third-party import identifiers present in after but
// not in before, skipping relative imports (a leading "."). It is a
// syntactic line scan across Go, Python, and JS/TS import shapes, not a
// parser, so an import split across lines or built dynamically is missed.
func importDelta(before, after string) []string {
	b := importIdentifiers(before)
	a := importIdentifiers(after)

	var added []string

	for id := range a {
		if !b[id] {
			added = append(added, id)
		}
	}

	sort.Strings(added)

	return added
}

func importIdentifiers(content string) map[string]bool {
	ids := make(map[string]bool)

	for _, line := range strings.Split(content, "\n") {
		for _, re := range importPatterns {
			m := re.FindStringSubmatch(line)
			if m == nil {
				continue
			}

			if id := m[1]; id != "" && !strings.HasPrefix(id, ".") {
				ids[id] = true
			}

			break
		}
	}

	return ids
}
