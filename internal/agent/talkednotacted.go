package agent

import (
	"strings"
	"unicode"
)

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
	"trying again",
	"retrying",
}

// retryPhrases are how a model closes a turn by promising another attempt
// it then never makes. Unlike announcePrefixes they match anywhere in the
// closing line, because the promise usually follows an apology on the same
// line ("Sorry about that, let me try again.").
var retryPhrases = []string{
	"let me try",
	"try again",
}

// editVerbs open or carry a task that asks for a change to the tree, matched
// as whole words in the task's first line so "add" fires and "adds" does not.
var editVerbs = wordSet("add append bump change convert create delete drop edit extract fix implement " +
	"insert introduce migrate move refactor remove rename replace rewrite split update upgrade write")

// questionWords open a task that asks for an answer rather than a change,
// which keeps "how do I add a routine" out of the edit-shaped set even
// though it names an edit verb.
var questionWords = wordSet("are can could describe do does explain how is list should show summarize " +
	"tell what when where which who why would")

func wordSet(words string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, w := range strings.Fields(words) {
		out[w] = struct{}{}
	}

	return out
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
	for _, phrase := range retryPhrases {
		if strings.Contains(line, phrase) {
			return true
		}
	}

	return false
}

// looksLikeEditTask reports whether a task asks for a change to the tree,
// read from its first line: not a question, and carrying an edit verb as a
// whole word. A run that ends such a task having changed nothing has not
// done it, whatever its closing turn says and whether or not it ever
// reached for an edit tool.
func looksLikeEditTask(task string) bool {
	line := strings.ToLower(firstNonEmptyLine(task))
	if strings.HasSuffix(line, "?") {
		return false
	}

	words := strings.FieldsFunc(line, func(r rune) bool {
		return !unicode.IsLetter(r) && r != '\''
	})
	if len(words) == 0 {
		return false
	}
	if _, question := questionWords[words[0]]; question {
		return false
	}
	for _, w := range words {
		if _, edit := editVerbs[w]; edit {
			return true
		}
	}

	return false
}

func firstNonEmptyLine(text string) string {
	for line := range strings.SplitSeq(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}

	return ""
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
