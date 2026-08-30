package guard_test

import (
	"slices"
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

// A scoped sweep is what ProjectCheck passes through, so this is the half
// that names the packages: 148 of them went through the shell over 64
// recorded runs, and the caller answers only for those it already covered.
func TestGoPackageSweep(t *testing.T) {
	t.Parallel()

	tests := []struct {
		command string
		want    []string
	}{
		{command: "go test ./internal/bench", want: []string{"internal/bench"}},
		{command: "go test ./internal/config/...", want: []string{"internal/config"}},
		{command: "go build ./cmd/wavez", want: []string{"cmd/wavez"}},
		{command: "go vet ./internal/edit ./internal/tools", want: []string{"internal/edit", "internal/tools"}},
		{command: "go test -v ./internal/reduce", want: []string{"internal/reduce"}},
		{command: "mise exec -- go test ./internal/gate", want: []string{"internal/gate"}},
		{command: "go test -run TestOne ./internal/tui", want: nil},
		{command: "go test -run=TestOne ./internal/tui", want: nil},
		{command: "go test ./...", want: nil},
		{command: "go doc ./internal/edit", want: nil},
		{command: "", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			t.Parallel()

			got, ok := guard.GoPackageSweep(tt.command)
			if ok != (len(tt.want) > 0) {
				t.Fatalf("GoPackageSweep(%q) recognized = %v, want %v (got %q)", tt.command, ok, len(tt.want) > 0, got)
			}

			if !slices.Equal(got, tt.want) {
				t.Errorf("GoPackageSweep(%q) = %q, want %q", tt.command, got, tt.want)
			}
		})
	}
}
