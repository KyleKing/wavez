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

// announcePrefixes are how a model commits to a next action and then ends
// its turn without taking it. Matched only as a prefix of the closing line,
// since the same words mid-paragraph usually precede the action itself.
var announcePrefixes = []string{
	"i'll ",
	"i will ",
	"let me ",
	"now i'll ",
	"next, i",
	"let's ",
}

// looksLikeQuestionToUser reports whether text ends by offering the user
// more work rather than doing it. A trailing question mark alone is not
// enough, since a turn may close by restating what it was asked; an offer
// phrase must carry the closing line.
func looksLikeQuestionToUser(text string) bool {
	line := strings.ToLower(lastNonEmptyLine(text))
	if !strings.HasSuffix(line, "?") {
		return false
	}

	for _, phrase := range offerPhrases {
		if strings.Contains(line, phrase) {
			return true
		}
	}

	return false
}

// looksLikeAnnouncedAction reports whether text closes by committing to a
// next step. A turn that ends this way with no tool call has described work
// rather than done it, and the description is not the work.
func looksLikeAnnouncedAction(text string) bool {
	line := strings.ToLower(lastNonEmptyLine(text))
	if strings.HasSuffix(line, "?") {
		return false
	}

	for _, prefix := range announcePrefixes {
		if strings.HasPrefix(line, prefix) {
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
