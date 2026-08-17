package agent

import "strings"

// offerPhrases are how a model offers to do work instead of doing it. Each
// is matched only on a turn's closing line, and only when that line is a
// question, because the same words appear harmlessly mid-summary.
var offerPhrases = []string{
	"would you like",
	"do you want",
	"shall i",
	"should i",
	"want me to",
	"do you need",
	"let me know if",
}

// looksLikeQuestionToUser reports whether text ends by offering the user
// more work rather than doing it. A trailing question mark alone is not
// enough, since a turn may close by restating what it was asked; an offer
// phrase must carry the closing line.
func looksLikeQuestionToUser(text string) bool {
	line := lastNonEmptyLine(text)
	if !strings.HasSuffix(line, "?") {
		return false
	}

	lower := strings.ToLower(line)
	for _, phrase := range offerPhrases {
		if strings.Contains(lower, phrase) {
			return true
		}
	}

	return false
}

func lastNonEmptyLine(text string) string {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}

	return ""
}
