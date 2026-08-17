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
