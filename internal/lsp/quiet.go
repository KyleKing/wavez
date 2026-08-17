package lsp

import (
	"context"
	"log/slog"
	"strings"
)

// closedPipeMessage is what powernap logs at Error level when a language
// server exits and its stderr pipe closes underneath the reader. It is the
// ordinary end of a client's life, not a failure, and powernap takes no
// logger of its own, so filtering the record is the only place to stop it.
const closedPipeMessage = "Error reading stderr"

// QuietHandler drops the one benign record powernap logs on every client
// close and passes everything else to the wrapped handler, so a real
// language-server error still reaches the user.
type QuietHandler struct{ slog.Handler }

// Quiet wraps h so records naming a closed stderr pipe are dropped. Install
// it with slog.SetDefault in a binary's setup, never from a library.
func Quiet(h slog.Handler) *QuietHandler { return &QuietHandler{Handler: h} }

// Handle implements slog.Handler.
func (q *QuietHandler) Handle(ctx context.Context, rec slog.Record) error {
	if strings.Contains(rec.Message, closedPipeMessage) {
		return nil
	}

	//nolint:wrapcheck // a pass-through handler must not decorate the wrapped error
	return q.Handler.Handle(ctx, rec)
}

// WithAttrs implements slog.Handler.
func (q *QuietHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &QuietHandler{Handler: q.Handler.WithAttrs(attrs)}
}

// WithGroup implements slog.Handler.
func (q *QuietHandler) WithGroup(name string) slog.Handler {
	return &QuietHandler{Handler: q.Handler.WithGroup(name)}
}
