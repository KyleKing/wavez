package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sampleDiff = `diff --git a/internal/lease/lease.go b/internal/lease/lease.go
index 1234567..89abcde 100644
--- a/internal/lease/lease.go
+++ b/internal/lease/lease.go
@@ -10,4 +10,5 @@ package lease
 const prefix = "lease"
-const DefaultTTL = 30 * time.Minute
+func TTL(cfg Config) time.Duration {
+	return cfg.TTL
+}
`

// wavez writes its gate log and index under .wavez on every run, so those
// files show up in a thread's diff as work the thread did.
func TestParseDiff_DropsWavezOwnState(t *testing.T) {
	t.Parallel()

	const withState = `diff --git a/.wavez/gate.log b/.wavez/gate.log
--- a/.wavez/gate.log
+++ b/.wavez/gate.log
@@ -1,1 +1,2 @@
 {"gate":"format"}
+{"gate":"go-test"}
diff --git a/greet.go b/greet.go
--- a/greet.go
+++ b/greet.go
@@ -3,1 +3,1 @@
-const Greeting = "hi"
+const Greeting = "howdy"
`

	for _, r := range parseDiff(withState) {
		assert.NotContains(t, r.File, ".wavez/", "row %q leaked wavez state into the diff", r.Text)
	}

	assert.NotEmpty(t, parseDiff(withState), "the real change must survive the filter")
}

func TestParseDiff_AnchorsRowsToPostImageLines(t *testing.T) {
	t.Parallel()

	rows := parseDiff(sampleDiff)
	require.NotEmpty(t, rows)

	assert.Equal(t, diffFile, rows[0].Kind)
	assert.Equal(t, "internal/lease/lease.go", rows[0].File)

	var (
		added   []string
		removed int
	)

	for _, r := range rows {
		switch r.Kind {
		case diffAdd:
			added = append(added, r.anchor())
		case diffRemove:
			removed++
			// A removed line has no line in the tree as it is now, so it
			// anchors to the file alone rather than to a line that moved.
			assert.Equal(t, "internal/lease/lease.go", r.anchor())
		case diffFile, diffHunk, diffContext:
		}
	}

	assert.Equal(t, 1, removed)
	// Context line 10, then the three added lines follow it.
	assert.Equal(t, []string{
		"internal/lease/lease.go:11",
		"internal/lease/lease.go:12",
		"internal/lease/lease.go:13",
	}, added)
}
