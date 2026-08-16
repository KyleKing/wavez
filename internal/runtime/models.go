package runtime

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

// ErrMalformedOllamaList is returned by ParseOllamaList when a non-header
// line does not have Ollama's expected NAME/ID/SIZE/MODIFIED columns.
var ErrMalformedOllamaList = errors.New("runtime: malformed ollama list output")

// Model is one entry from `ollama list`: name, size, and modified time as
// Ollama itself reports them. Size and Modified are kept as Ollama's own
// human-readable strings ("5.2 GB", "3 days ago") rather than parsed into
// bytes or a time.Time, since `ollama list` reports both already rounded
// and relative, and re-parsing would only manufacture false precision.
type Model struct {
	Name     string
	Size     string
	Modified string
}

// columnGap splits `ollama list`'s padded columns, which are aligned with
// runs of two or more spaces rather than a single delimiter.
var columnGap = regexp.MustCompile(`\s{2,}`)

// ollamaListColumns is `ollama list`'s fixed column count: NAME, ID, SIZE,
// MODIFIED.
const ollamaListColumns = 4

// ListModels runs `ollama list` and parses its output.
func ListModels(ctx context.Context) ([]Model, error) {
	cmd := exec.CommandContext(ctx, "ollama", "list")

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("running ollama list: %w", err)
	}

	return ParseOllamaList(out)
}

// ParseOllamaList parses `ollama list`'s tabular output. The header line
// (NAME/ID/SIZE/MODIFIED) is detected and skipped rather than assumed
// present, so a caller can also feed it a header-less capture.
func ParseOllamaList(out []byte) ([]Model, error) {
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")

	var models []Model

	for _, line := range lines {
		if strings.TrimSpace(line) == "" || isOllamaListHeader(line) {
			continue
		}

		fields := columnGap.Split(strings.TrimSpace(line), -1)
		if len(fields) < ollamaListColumns {
			return nil, fmt.Errorf("%w: %q", ErrMalformedOllamaList, line)
		}

		models = append(models, Model{Name: fields[0], Size: fields[2], Modified: fields[3]})
	}

	return models, nil
}

func isOllamaListHeader(line string) bool {
	return strings.HasPrefix(strings.TrimSpace(line), "NAME")
}
