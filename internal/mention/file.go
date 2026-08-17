package mention

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrPathOutsideRoot reports a mention naming a path outside the project
// root.
var ErrPathOutsideRoot = errors.New("path is outside the project root")

// expansion is what one reference produced: the mentions to report, the
// block to append to the prompt, and the lines of file content it spent from
// the shared budget. An unhandled expansion means no file answered to the
// reference, the one case that falls through to a symbol lookup.
type expansion struct {
	section  string
	mentions []Mention
	lines    int
	handled  bool
}

func (e *Expander) resolveFile(ref string, budget int) expansion {
	abs, err := resolveInRoot(e.root, ref)
	if err != nil {
		return note(ref, fmt.Sprintf("%v; left as literal text", err))
	}

	info, err := os.Stat(abs)
	switch {
	case err != nil:
		if !strings.Contains(ref, "/") {
			return expansion{}
		}

		return note(ref, "no file at "+ref+"; left as literal text")
	case info.IsDir():
		return note(ref, ref+" is a directory; name a file inside it")
	}

	return e.expandFile(ref, abs, budget)
}

func (e *Expander) expandFile(ref, abs string, budget int) expansion {
	data, err := os.ReadFile(abs) // #nosec G304 -- abs is resolved and root-checked above
	if err != nil {
		return note(ref, fmt.Sprintf("reading %s failed: %v; left as literal text", ref, err))
	}

	if bytes.IndexByte(data, 0) >= 0 {
		return fileNote(ref, fmt.Sprintf("binary file (%d bytes), not expanded", len(data)))
	}

	lines := splitLines(string(data))

	allowed := min(e.fileLines, budget)
	if allowed <= 0 {
		return fileNote(ref, fmt.Sprintf(
			"%s, not expanded: the %d-line budget for one prompt is already spent; use read",
			lineCount(len(lines)), e.totalLines))
	}

	if len(lines) <= allowed {
		return expansion{
			mentions: []Mention{{Ref: ref, Kind: KindFile, Detail: lineCount(len(lines))}},
			section: fmt.Sprintf("@%s (file, %s):\n%s",
				ref, lineCount(len(lines)), strings.Join(lines, "\n")),
			lines:   len(lines),
			handled: true,
		}
	}

	return expansion{
		mentions: []Mention{{Ref: ref, Kind: KindFile, Truncated: true, Detail: fmt.Sprintf(
			"%s, expanded 1-%d against a %d-line budget", lineCount(len(lines)), allowed, e.fileLines)}},
		section: fmt.Sprintf(
			"@%s (file, %s, showing 1-%d; the %d-line mention budget cut it):\n%s\n"+
				"[%d lines not shown; use read with a line range for the rest]",
			ref, lineCount(len(lines)), allowed, e.fileLines,
			strings.Join(lines[:allowed], "\n"), len(lines)-allowed),
		lines:   allowed,
		handled: true,
	}
}

func lineCount(n int) string {
	if n == 1 {
		return "1 line"
	}

	return fmt.Sprintf("%d lines", n)
}

// splitLines drops the empty element a trailing newline leaves behind, so a
// budget counts lines of content rather than line terminators.
func splitLines(content string) []string {
	lines := strings.Split(content, "\n")
	if len(lines) > 1 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	return lines
}

// resolveInRoot resolves ref against root and refuses one that lexically
// escapes it, whether given as an absolute path or a relative one that walks
// out with "..".
func resolveInRoot(root, ref string) (string, error) {
	if ref == "" {
		return "", fmt.Errorf("%w: empty path", ErrPathOutsideRoot)
	}

	abs := filepath.Clean(ref)
	if !filepath.IsAbs(ref) {
		abs = filepath.Clean(filepath.Join(root, ref))
	}

	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %s", ErrPathOutsideRoot, ref)
	}

	return abs, nil
}

func note(ref, detail string) expansion {
	return oneLine(Mention{Ref: ref, Kind: KindUnresolved, Detail: detail})
}

func fileNote(ref, detail string) expansion {
	return oneLine(Mention{Ref: ref, Kind: KindFile, Truncated: true, Detail: detail})
}

func oneLine(m Mention) expansion {
	return expansion{
		mentions: []Mention{m},
		section:  fmt.Sprintf("@%s (%s): %s", m.Ref, m.Kind, m.Detail),
		handled:  true,
	}
}
