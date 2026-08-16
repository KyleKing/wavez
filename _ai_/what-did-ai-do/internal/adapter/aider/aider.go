// Package aider parses Aider's per-project Markdown chat history file into
// the agent-agnostic session.Session representation.
package aider

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kyleking/what-did-ai-do/internal/session"
)

const (
	historyFileName = ".aider.chat.history.md"
	timestampLayout = "2006-01-02 15:04:05"
	toolCallName    = "aider-edit"
)

var (
	sessionHeaderRe = regexp.MustCompile(`(?m)^# aider chat started at (.+)$`)
	slugNonAlnumRe  = regexp.MustCompile(`[^a-zA-Z0-9]+`)
)

// Discover returns the Aider chat history file path under projectPath, if
// present. It returns an empty slice (not an error) when no history file
// exists.
func Discover(projectPath string) ([]string, error) {
	path := filepath.Join(projectPath, historyFileName)

	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("checking for aider chat history at %s: %w", path, err)
	}

	return []string{path}, nil
}

// Parse reads the Aider chat history file at path and returns one
// session.Session per "# aider chat started at ..." block it contains.
// Blocks with an unparseable header timestamp are skipped rather than
// failing the whole file; a truncated trailing edit block is parsed as far
// as possible with the incomplete edit dropped.
func Parse(path string) ([]session.Session, error) {
	//nolint:gosec // path is the caller-supplied chat history file to parse, not user input to validate
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading aider chat history %s: %w", path, err)
	}

	projectPath := filepath.Dir(path)
	content := string(data)

	headerLocs := sessionHeaderRe.FindAllStringSubmatchIndex(content, -1)
	if len(headerLocs) == 0 {
		return nil, nil
	}

	var sessions []session.Session

	for i, loc := range headerLocs {
		start := loc[0]
		end := len(content)
		if i+1 < len(headerLocs) {
			end = headerLocs[i+1][0]
		}

		timestampStr := content[loc[2]:loc[3]]

		startedAt, err := time.Parse(timestampLayout, strings.TrimSpace(timestampStr))
		if err != nil {
			continue
		}

		sessions = append(
			sessions,
			parseSessionBlock(content[start:end], timestampStr, startedAt, projectPath),
		)
	}

	return sessions, nil
}

func parseSessionBlock(
	block, timestampStr string,
	startedAt time.Time,
	projectPath string,
) session.Session {
	lines := strings.Split(block, "\n")

	var messages []session.Message

	var toolCalls []session.ToolCall

	pendingFilePath := ""

	for i := 1; i < len(lines); {
		trimmed := strings.TrimSpace(lines[i])

		switch {
		case trimmed == "":
			i++
		case strings.HasPrefix(trimmed, ">"):
			i++
		case strings.HasPrefix(trimmed, "#### "):
			var text string
			text, i = collectUserMessage(lines, i)
			messages = append(messages, session.Message{At: startedAt, Role: "user", Text: text})
		case strings.HasPrefix(trimmed, "```"):
			var toolCall *session.ToolCall
			toolCall, i = collectEditBlock(lines, i, startedAt, pendingFilePath)
			pendingFilePath = ""

			if toolCall != nil {
				toolCalls = append(toolCalls, *toolCall)
			}
		case i+1 < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i+1]), "```"):
			pendingFilePath = trimmed
			i++
		default:
			var text string
			text, i = collectProse(lines, i)

			if text != "" {
				messages = append(
					messages,
					session.Message{At: startedAt, Role: "assistant", Text: text},
				)
			}
		}
	}

	return session.Session{
		ID:          slugifyTimestamp(timestampStr),
		Agent:       session.AgentAider,
		ProjectPath: projectPath,
		StartedAt:   startedAt,
		Messages:    messages,
		ToolCalls:   toolCalls,
	}
}

//nolint:gocritic // named results would conflict with nonamedreturns
func collectUserMessage(lines []string, i int) (string, int) {
	var buf []string

	for i < len(lines) && strings.TrimSpace(lines[i]) != "" {
		buf = append(
			buf,
			strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i]), "#### ")),
		)
		i++
	}

	return strings.Join(buf, "\n"), i
}

//nolint:gocritic // named results would conflict with nonamedreturns
func collectEditBlock(
	lines []string,
	i int,
	startedAt time.Time,
	filePath string,
) (*session.ToolCall, int) {
	fenceLine := lines[i]
	i++

	var body []string

	closed := false

	for i < len(lines) {
		if strings.TrimSpace(lines[i]) == "```" {
			closed = true
			i++

			break
		}

		body = append(body, lines[i])
		i++
	}

	if !closed {
		return nil, i
	}

	bodyText := strings.Join(body, "\n")
	if !strings.Contains(bodyText, "<<<<<<< SEARCH") ||
		!strings.Contains(bodyText, ">>>>>>> REPLACE") {
		return nil, i
	}

	var files []string
	if filePath != "" {
		files = []string{filePath}
	}

	full := fenceLine + "\n" + bodyText + "\n```"

	return &session.ToolCall{
		At:     startedAt,
		Name:   toolCallName,
		Input:  full,
		Output: full,
		Files:  files,
	}, i
}

//nolint:gocritic // named results would conflict with nonamedreturns
func collectProse(lines []string, i int) (string, int) {
	var buf []string

	for i < len(lines) {
		t := strings.TrimSpace(lines[i])
		if isProseBoundary(t) {
			break
		}

		buf = append(buf, t)
		i++
	}

	return strings.Join(buf, " "), i
}

func isProseBoundary(t string) bool {
	return t == "" || strings.HasPrefix(t, ">") || strings.HasPrefix(t, "```") ||
		strings.HasPrefix(t, "#### ") || strings.HasPrefix(t, "# ")
}

func slugifyTimestamp(ts string) string {
	slug := slugNonAlnumRe.ReplaceAllString(strings.TrimSpace(ts), "-")
	return strings.Trim(slug, "-")
}
