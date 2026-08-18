package agent

import "testing"

func TestLooksLikeQuestionToUser(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want bool
	}{
		{
			name: "the closing offer a real run produced",
			text: "I have made the requested changes.\n\nWould you like me to run the tests to verify this?",
			want: true,
		},
		{name: "shall i", text: "Both files are edited. Shall I run the gates?", want: true},
		{name: "trailing blank lines do not hide the offer", text: "Do you want me to continue?\n\n\n", want: true},
		{name: "offer without a question mark", text: "Let me know if you want the tests run.", want: false},
		{name: "an offer that is not the closing line", text: "Should I rename it? I renamed it.", want: false},
		{name: "a question that offers nothing", text: "Was that the rename you meant?", want: false},
		{name: "an ordinary report", text: "Renamed DefaultTTL to TTL across 3 files.", want: false},
		{name: "empty", text: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := looksLikeQuestionToUser(tt.text); got != tt.want {
				t.Errorf("looksLikeQuestionToUser(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}

func TestLooksLikeAnnouncedAction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		text string
		want bool
	}{
		{
			name: "the closing announcement a real run produced",
			text: "The error indicates a permission issue.\n\nI'll start by running the `hk check --all` command.",
			want: true,
		},
		{name: "let me", text: "Let me check the test output first.", want: true},
		{
			name: "let me try again",
			text: "That edit failed.\n\nLet me try again with a wider anchor.",
			want: true,
		},
		{name: "trying again", text: "The build still fails.\n\nTrying again with a different approach.", want: true},
		{name: "retrying", text: "str_replace errored again.\n\nRetrying the call.", want: true},
		{
			name: "an announcement that does not open the closing line",
			text: "Fixed in lease.go, and I'll stop there.",
			want: false,
		},
		{name: "a closing question is the other detector's job", text: "Shall I run the tests?", want: false},
		{name: "an ordinary report", text: "Renamed DefaultTTL to TTL across 3 files.", want: false},
		{name: "empty", text: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := looksLikeAnnouncedAction(tt.text); got != tt.want {
				t.Errorf("looksLikeAnnouncedAction(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}
}
