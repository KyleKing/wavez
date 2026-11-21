package reduce_test

import (
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/reduce"
)

func TestBound(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a.go:1:1: first cause\n", 40) + strings.Repeat("FAIL pkg verdict\n", 40)

	tests := []struct {
		name  string
		in    string
		holds []string
		limit int
		whole bool
	}{
		{name: "text within the limit is returned whole", in: "short\n", limit: 100, whole: true},
		{name: "a zero limit means unbounded", in: long, limit: 0, whole: true},
		{
			name: "long text keeps both ends on line boundaries", in: long, limit: 200,
			holds: []string{"a.go:1:1: first cause\n...", "bytes cut] ...\nFAIL pkg verdict\n"},
		},
		{
			name: "one long line is cut mid-line rather than dropped", in: strings.Repeat("x", 500), limit: 100,
			holds: []string{"[400 bytes cut]"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := reduce.Bound(tt.in, tt.limit)
			if tt.whole {
				if got != tt.in {
					t.Fatalf("changed text that fit:\n%s", got)
				}

				return
			}

			const marker = 40
			if len(got) > tt.limit+marker {
				t.Errorf("bounded to %d bytes, want at most %d", len(got), tt.limit+marker)
			}

			for _, want := range tt.holds {
				if !strings.Contains(got, want) {
					t.Errorf("missing %q in:\n%s", want, got)
				}
			}
		})
	}
}
