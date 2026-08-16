package greet

import "testing"

func TestHello(t *testing.T) {
	got := Hello("wavez")
	want := "hello, wavez"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
