package covmod

import "testing"

func TestGreetNamed(t *testing.T) {
	if got := Greet("Ada"); got != "hello, Ada" {
		t.Fatalf("got %q", got)
	}
}

func TestGreetEmpty(t *testing.T) {
	if got := Greet(""); got != "hello, stranger" {
		t.Fatalf("got %q", got)
	}
}
