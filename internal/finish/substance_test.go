package finish_test

import (
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/finish"
)

// A run can satisfy every other bound by doing nothing. Measured on `h6`: a
// run added a comment saying it had ensured a character boundary, left the
// code alone, and reported complete with every gate green.
func TestChangeHasSubstance(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		diff   string
		wantOK bool
	}{
		{
			name: "a comment above untouched code is not the work",
			diff: "--- a/thread.go\n+++ b/thread.go\n@@ -1 +1,2 @@\n" +
				"+\t// Ensure we truncate on a character boundary\n \treturn s[:limit]\n",
		},
		{
			name: "code beside a comment is the work",
			diff: "--- a/thread.go\n+++ b/thread.go\n@@ -1 +1,2 @@\n" +
				"+\t// cut on a rune boundary\n+\tfor !utf8.RuneStart(s[limit]) {\n",
			wantOK: true,
		},
		{
			name:   "a deletion of code counts",
			diff:   "--- a/x.go\n+++ b/x.go\n@@ -1 +0,0 @@\n-\treturn s[:limit]\n",
			wantOK: true,
		},
		{
			name:   "an empty diff says nothing, since another bound reports it",
			diff:   "",
			wantOK: true,
		},
		{
			name:   "the file headers are not read as edits",
			diff:   "--- a/x.go\n+++ b/x.go\n",
			wantOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			report := finish.ChangeHasSubstance(tt.diff)
			if report.OK() != tt.wantOK {
				t.Fatalf("OK() = %v, want %v:\n%s", report.OK(), tt.wantOK, report)
			}

			if !tt.wantOK && !strings.Contains(report.String(), "comments") {
				t.Errorf("report = %q, want it to say what it found", report)
			}
		})
	}
}
