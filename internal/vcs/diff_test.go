package vcs_test

import (
	"reflect"
	"testing"

	"github.com/kyleking/wavez/internal/tool"
	"github.com/kyleking/wavez/internal/vcs"
)

func TestChangedRanges(t *testing.T) {
	t.Parallel()

	const diff = `diff --git a/one.go b/one.go
--- a/one.go
+++ b/one.go
@@ -3,4 +3,6 @@ func One() {
 context
+added
+added
@@ -20 +22 @@
-old
+new
diff --git a/two.go b/two.go
--- a/two.go
+++ b/two.go
@@ -10,3 +10,0 @@
-gone
-gone
-gone
`

	want := map[string][]tool.LineRange{
		"one.go": {{Start: 3, End: 8}, {Start: 22, End: 22}},
	}

	got := vcs.ChangedRanges(diff)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ChangedRanges = %v, want %v (a pure deletion leaves no line to check)", got, want)
	}
}
