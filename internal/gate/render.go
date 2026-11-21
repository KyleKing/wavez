package gate

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"slices"
	"strings"
)

// fingerprintLen is how many hex digits of the hash a fingerprint keeps. It
// is compared against the previous round's, never looked up, so 64 bits is
// plenty.
const fingerprintLen = 16

// elapsed matches the durations `go test` stamps on its verdicts. They vary
// between two runs of the same failure, so they are kept out of the
// fingerprint.
var elapsed = regexp.MustCompile(`\b\d+(\.\d+)?(ns|µs|ms|s|m|h)\b`)

// Failed reports whether any blocking check failed.
func (r Report) Failed() bool {
	return slices.ContainsFunc(r.Results, Result.blocks)
}

func (r Result) blocks() bool {
	return r.Blocking && r.Status == StatusFail
}

// Fingerprint identifies the set of blocking failures, so a caller can tell
// a repeated failure from a new one. It is empty when nothing blocks, does
// not depend on the order the checks reported in, and ignores the elapsed
// times a test run prints.
func (r Report) Fingerprint() string {
	var parts []string

	for _, res := range r.Results {
		if res.blocks() {
			parts = append(parts, res.Check+"\x00"+elapsed.ReplaceAllString(res.Summary, ""))
		}
	}

	if len(parts) == 0 {
		return ""
	}

	slices.Sort(parts)

	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00\x00")))

	return hex.EncodeToString(sum[:])[:fingerprintLen]
}

// Render is the text an agent reads. It is empty when there is nothing the
// agent needs to know: every check passed and no file was rewritten. Blocking
// failures come first, then advisory ones, then one line per check that could
// not run, then one line naming the files a fixer rewrote.
func (r Report) Render() string {
	var blocking, advisory []string

	for _, res := range r.Results {
		switch {
		case res.blocks():
			blocking = append(blocking, section("FAIL "+res.Check, res.Summary))
		case res.Status == StatusFail:
			advisory = append(advisory, section("ADVISORY "+res.Check+" (does not block)", res.Summary))
		}
	}

	lines := slices.Concat(blocking, advisory)

	if fixed := r.fixed(); len(fixed) > 0 {
		lines = append(lines, "REWRITTEN by a formatter, re-read before editing: "+strings.Join(fixed, " "))
	}

	if len(lines) == 0 {
		return ""
	}

	return strings.Join(lines, "\n") + "\n"
}

// Notes is the text for the person rather than the agent: one line per
// check that could not run. It is empty when every check ran. An agent
// handed these lines goes looking for the missing tool, which is work the
// change never asked for.
func (r Report) Notes() string {
	var lines []string

	for _, res := range r.Results {
		if res.Status == StatusAbstained {
			lines = append(lines, "NOT CHECKED "+res.Check+": "+res.Summary)
		}
	}

	if len(lines) == 0 {
		return ""
	}

	return strings.Join(lines, "\n") + "\n"
}

// fixed returns every rewritten file once, sorted.
func (r Report) fixed() []string {
	var out []string

	for _, res := range r.Results {
		out = append(out, res.Fixed...)
	}

	slices.Sort(out)

	return slices.Compact(out)
}

func section(header, summary string) string {
	summary = strings.TrimRight(summary, "\n")
	if summary == "" {
		return header
	}

	return header + "\n" + summary
}
