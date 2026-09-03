package tools

import (
	"fmt"
	"strings"
	"testing"
)

// A grouped summary is longer than the head-and-tail window and is bounded by
// the number of kinds rather than by the size of the run, so windowing it
// would drop the rarest kinds while claiming to have shown the output.
func TestTrimOutputKeepsWholeGroupedSummary(t *testing.T) {
	t.Parallel()

	var raw strings.Builder

	for kind := range 24 {
		for site := range 5 {
			fmt.Fprintf(&raw, "src/pkg/file%d.py:%d:1: R%03d rule %d fired here\n", kind, site+1, kind, kind)
		}
	}

	got := trimOutput(raw.String(), nil)

	if strings.Contains(got, "lines omitted") {
		t.Errorf("windowed a complete summary:\n%s", got)
	}

	// 11 sits in the stretch a head-and-tail window drops.
	for _, kind := range []int{0, 11, 23} {
		if want := fmt.Sprintf("R%03d rule %d fired here", kind, kind); !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}
