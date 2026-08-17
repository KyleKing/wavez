package strong

import "testing"

func TestPassing(t *testing.T) {
	for _, tt := range []struct {
		score int
		want  bool
	}{{59, false}, {60, true}, {61, true}} {
		if got := Passing(tt.score); got != tt.want {
			t.Errorf("Passing(%d) = %v, want %v", tt.score, got, tt.want)
		}
	}
}
