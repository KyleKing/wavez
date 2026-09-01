package guard

import "testing"

// TestSplitSequenceRedirectionAmpersand covers the trigger: a `2>&1`
// redirection must not be split as a backgrounding `&`, leaving a phantom
// command named `1`.
func TestSplitSequenceRedirectionAmpersand(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "stderr redirected to stdout",
			in:   `go build ./... 2>&1 | head`,
			want: []string{`go build ./... 2>&1 | head`},
		},
		{
			name: "stdout and stderr redirection",
			in:   `echo hi &>log`,
			want: []string{`echo hi &>log`},
		},
		{
			name: "bare fd duplication",
			in:   `cmd >&2`,
			want: []string{`cmd >&2`},
		},
		{
			name: "real backgrounding still splits",
			in:   `sleep 1 & echo done`,
			want: []string{`sleep 1`, `echo done`},
		},
		{
			name: "ampersand inside quotes is untouched",
			in:   `echo "a&b"`,
			want: []string{`echo "a&b"`},
		},
		{
			name: "mixed backgrounding and redirection",
			in:   `make 2>&1 & wait`,
			want: []string{`make 2>&1`, `wait`},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := splitSequence(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("splitSequence(%q) = %q, want %q", tt.in, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("splitSequence(%q) = %q, want %q", tt.in, got, tt.want)
				}
			}
		})
	}
}
