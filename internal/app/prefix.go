package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ErrSectionNotFound reports a "file#Heading" context entry whose heading
// does not appear in file.
var ErrSectionNotFound = errors.New("app: heading not found")

// headingPattern matches a Markdown ATX heading line, capturing its level
// (number of '#') and title text.
var headingPattern = regexp.MustCompile(`^(#{1,6})\s+(.*?)\s*$`)

// BuildPrefix reads each context entry relative to root and joins them into
// the stable system prefix Prefix.System carries for the life of a thread.
// An entry is either a whole file ("AGENTS.md") or one Markdown section
// within it ("AGENTS.md#architecture"), extracted as everything under that
// heading up to the next heading of the same or shallower level. Wavez
// never reads a file that is not named here: this is the only path project
// instructions enter the model's context, besides a caller's --with
// override.
func BuildPrefix(root string, entries []string) (string, error) {
	sections := make([]string, 0, len(entries))

	for _, entry := range entries {
		section, err := readContextEntry(root, entry)
		if err != nil {
			return "", err
		}

		sections = append(sections, section)
	}

	return strings.Join(sections, "\n\n"), nil
}

func readContextEntry(root, entry string) (string, error) {
	file, heading, hasHeading := strings.Cut(entry, "#")

	//nolint:gosec // path comes from the project's own .wavez.pkl, not model input
	data, err := os.ReadFile(filepath.Join(root, file))
	if err != nil {
		return "", fmt.Errorf("reading context entry %q: %w", entry, err)
	}

	if !hasHeading {
		return string(data), nil
	}

	section, err := extractSection(string(data), heading)
	if err != nil {
		return "", fmt.Errorf("context entry %q: %w", entry, err)
	}

	return section, nil
}

// extractSection returns the content under the Markdown heading titled
// heading (matched case-insensitively) in content, stopping at the next
// heading whose level is the same or shallower.
func extractSection(content, heading string) (string, error) {
	lines := strings.Split(content, "\n")

	level, start := findHeading(lines, heading)
	if start == -1 {
		return "", fmt.Errorf("%w: %q", ErrSectionNotFound, heading)
	}

	end := len(lines)

	for i := start; i < len(lines); i++ {
		m := headingPattern.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}

		if len(m[1]) <= level {
			end = i

			break
		}
	}

	return strings.TrimSpace(strings.Join(lines[start:end], "\n")), nil
}

// findHeading returns the level (number of '#') and the index of the line
// after the first heading matching heading, or start -1 if none matches.
func findHeading(lines []string, heading string) (int, int) {
	for i, line := range lines {
		m := headingPattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}

		if !strings.EqualFold(m[2], heading) {
			continue
		}

		return len(m[1]), i + 1
	}

	return 0, -1
}
