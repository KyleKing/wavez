package guard_test

import (
	"testing"

	"github.com/kyleking/wavez/internal/guard"
)

// The system prompt has said not to re-run the project's checks since the
// ChangeGate shipped, and 37 of 278 logged shell calls did it anyway. The
// line this classifier has to hold is the one the prompt already draws:
// a sweep of the module is a report the harness has, and one package's
// tests are work.
func TestProjectCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		command string
		want    string
	}{
		{command: "go test ./...", want: "tests"},
		{command: "go build ./...", want: "build"},
		{command: "go vet ./...", want: "vet"},
		{command: "mise exec -- golangci-lint run ./...", want: "lint"},
		{command: "mise run ci", want: "the mise task"},
		{command: "hk check --all", want: "the hook pipeline"},
		{command: "gofmt -l .", want: "format"},
		{command: "cat x.go && go test ./...", want: "tests"},
		{command: "go test ./internal/edit/...", want: ""},
		{command: "go test -run TestOne ./internal/tui", want: ""},
		{command: "go doc ./...", want: ""},
		{command: "grep -rn ProjectCheck internal", want: ""},
		{command: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			t.Parallel()

			got, ok := guard.ProjectCheck(tt.command)
			if ok != (tt.want != "") {
				t.Fatalf("ProjectCheck(%q) recognized = %v, want %v (got %q)", tt.command, ok, tt.want != "", got)
			}

			if got != tt.want {
				t.Errorf("ProjectCheck(%q) = %q, want %q", tt.command, got, tt.want)
			}
		})
	}
}

// A stream editor writing files back edits behind the harness, and the run
// that reached for one after `rename` spent seven shell calls on two comment
// lines.
func TestInPlaceEdit(t *testing.T) {
	t.Parallel()

	tests := map[string]bool{
		"sed -i '' -e 's/a/b/' f.go":                   true,
		"sed -i.bak 's/a/b/' f.go":                     true,
		"perl -pi -e 's/a/b/' f.go":                    true,
		"perl --in-place 's/a/b/' f.go":                true,
		"go build ./... && sed -i '' -e 's/a/b/' f.go": true,
		"sed -n '1,20p' f.go":                          false,
		"sed -e 's/a/b/' f.go > g.go":                  false,
		"perl -e 'print 1'":                            false,
		"perl -Ilib script.pl":                         false,
		"grep -i pattern f.go":                         false,
	}

	for command, want := range tests {
		t.Run(command, func(t *testing.T) {
			t.Parallel()

			name, got := guard.InPlaceEdit(command)
			if got != want {
				t.Errorf("InPlaceEdit(%q) = %q, %v, want %v", command, name, got, want)
			}
		})
	}
}
