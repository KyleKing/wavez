package tools

import (
	"context"
	"crypto/sha256"
	"sync"

	"github.com/kyleking/wavez/internal/tool"
)

// SeenFiles remembers the bytes a run was last shown for a path, so an edit
// addressed by line number is checked against the state its caller saw rather
// than trusted.
//
// It is keyed by writer because one registry serves every thread, and an
// absent writer is the every-writer key a fixture or a one-off probe gets.
type SeenFiles struct {
	at map[string][32]byte
	mu sync.Mutex
}

// NewSeenFiles builds the store shared by the tools that show a file and the
// tools that edit one by line number.
func NewSeenFiles() *SeenFiles { return &SeenFiles{at: map[string][32]byte{}} }

// Note records that the run behind ctx has been shown data for abs.
func (s *SeenFiles) Note(ctx context.Context, abs string, data []byte) {
	if s == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.at[seenKey(ctx, abs)] = sha256.Sum256(data)
}

// Shown reports whether abs still holds the bytes the run behind ctx was shown.
// A path the run never read is not shown, so a line range it invented is
// refused rather than applied to a file it is recalling.
func (s *SeenFiles) Shown(ctx context.Context, abs string, data []byte) bool {
	if s == nil {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	was, ok := s.at[seenKey(ctx, abs)]

	return ok && was == sha256.Sum256(data)
}

func seenKey(ctx context.Context, abs string) string {
	writer, _ := tool.WriterFromContext(ctx)

	return writer + "\x00" + abs
}
