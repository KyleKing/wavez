package predicate

import "testing"

func TestParseErrorMessage(t *testing.T) {
	t.Parallel()
	err := &ParseError{Input: "dirty and", Message: "unexpected end of expression"}
	want := `parsing "dirty and": unexpected end of expression`
	if got := err.Error(); got != want {
		t.Errorf("expected %q, got %q", want, got)
	}
}
