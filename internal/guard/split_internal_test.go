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

// A heredoc body is data, and so is the terminator that closes it. The
// splitter reads a newline as a command separator, so a markdown file written
// through one had every heading classified as a command named `#`, which
// stopped the run for an approval nobody could answer.
func TestStripHeredocBodies(t *testing.T) {
	t.Parallel()

	tests := map[string]struct{ in, want string }{
		"quoted terminator": {
			in:   "cat > x.md <<'EOF'\n# Heading\nrm -rf /\nEOF\necho done",
			want: "cat > x.md <<'EOF'\necho done",
		},
		"bare terminator": {
			in:   "cat <<EOF\n# Heading\nEOF",
			want: "cat <<EOF",
		},
		"dash form": {
			in:   "cat <<-END\n\tbody\n\tEND\ntrue",
			want: "cat <<-END\ntrue",
		},
		"two on one line": {
			in:   "diff <(cat <<A\na\nA\n) x\n",
			want: "diff <(cat <<A\n) x\n",
		},
		"a << inside quotes opens nothing": {
			in:   "echo 'a << b'\nrm -rf /",
			want: "echo 'a << b'\nrm -rf /",
		},
		"an unterminated body ends at the last line": {
			in:   "cat <<EOF\nstill body\nmore body",
			want: "cat <<EOF",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := stripHeredocBodies(tt.in); got != tt.want {
				t.Errorf("stripHeredocBodies(%q) =\n%q\nwant\n%q", tt.in, got, tt.want)
			}
		})
	}
}
