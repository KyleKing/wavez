package fail

import "testing"

func TestGreet(t *testing.T) {
	if got := Greet("Ada"); got != "hello Ada" {
		t.Fatalf("Greet(%q) = %q, want %q", "Ada", got, "hello Ada")
	}
}
