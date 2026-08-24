package gofix_test

import (
	"testing"

	"github.com/kyleking/wavez/internal/gofix"
)

// One recorded e2 lane replaced a function's header line with a whole new
// function, orphaning the old body. The call reported success and the
// failure arrived from the test gate several turns later, which is the gate
// round this check is meant to save.
func TestBrokeSyntax(t *testing.T) {
	t.Parallel()

	const whole = "package a\n\nfunc F() {\n\tprintln(1)\n}\n"

	tests := []struct {
		name          string
		path          string
		before, after string
		want          bool
	}{
		{
			name: "an orphaned body is reported", path: "a.go",
			before: whole, after: "package a\n\nfunc G() {}\n\tprintln(1)\n}\n", want: true,
		},
		{name: "an intact edit is not", path: "a.go", before: whole, after: "package a\n\nfunc F() {}\n"},
		{
			name: "a file that was already broken stays the build gate's report", path: "a.go",
			before: "package a\n\nfunc F( {\n", after: "package a\n\nfunc F( {{\n",
		},
		{name: "a non-Go file is not parsed", path: "a.md", before: whole, after: "# not go {"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			msg, broke := gofix.BrokeSyntax(tt.path, []byte(tt.before), []byte(tt.after))
			if broke != tt.want {
				t.Fatalf("BrokeSyntax = %v (%q), want %v", broke, msg, tt.want)
			}
			if broke && msg == "" {
				t.Error("a reported break carried no parse error to act on")
			}
		})
	}
}
