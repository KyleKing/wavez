package tools

import "testing"

// The tty decides whether it echoes a control byte raw or as the two
// printable characters ECHOCTL renders it with: macOS shows `^[` where Linux
// shows the Escape itself. A matcher written for one read the other's echo as
// the program drawing, which ended the wait before the program had run.
func TestPtyScreenNote_ReadsEitherEchoForm(t *testing.T) {
	t.Parallel()

	reply := "\x1b[?2026;0$y"

	tests := map[string]struct {
		drawn      []byte
		wantAnswer bool
	}{
		"the echo raw":            {drawn: []byte(reply)},
		"the echo caret-rendered": {drawn: []byte("^[[?2026;0$y")},
		"the echo then the program": {
			drawn:      []byte("^[[?2026;0$yDREW"),
			wantAnswer: true,
		},
		"the program alone": {drawn: []byte("DREW"), wantAnswer: true},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := &ptyScreen{}
			s.expectEcho([]byte(reply))
			s.note(tt.drawn)

			if s.answer != tt.wantAnswer {
				t.Errorf("answer = %v after %q, want %v", s.answer, tt.drawn, tt.wantAnswer)
			}
		})
	}
}
