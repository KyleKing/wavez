// Package route answers one question about a prompt before the agent starts
// on it: is this direct work, exploration, or work to split. The answer
// becomes one fixed sentence of context, or nothing at all.
//
// The state the question is asked about is the prompt followed, when the
// prompt names paths that exist in the repository, by a blank line and one
// line listing them:
//
//	Rename Read in internal/bench/stats.go to ReadLog.
//
//	existing-paths: internal/bench/stats.go
//
// A directory in that list ends with a slash. Whether a path exists is
// something code can check, so State checks it and neither a rule nor a
// model has to guess. A line of a labeled corpus is one such state.
package route

import (
	"context"

	"github.com/kyleking/wavez/internal/decide"
)

// The members of the route set. Silent is the fallback: the hook says
// nothing, which is what happened before the question existed.
const (
	Direct     = "direct"
	Explore    = "explore"
	Coordinate = "coordinate"
	Silent     = "silent"
)

// QuestionName identifies the route question in the decision log, the
// cache, and the corpus.
const QuestionName = "route"

// The sentences an agent may be handed. They are constants, so nothing a
// model writes reaches the agent.
const (
	hintDirect = "wavez: this prompt names what to change and where, " +
		"so make the change without surveying the repository first."
	hintExplore = "wavez: this prompt does not say where its target is, " +
		"so find it with a targeted search before changing anything."
	hintCoordinate = "wavez: this prompt holds several independent pieces, " +
		"so plan them before starting and consider a subagent for each."
)

// Question returns the route question.
func Question() decide.Question {
	return decide.Question{
		Name: QuestionName,
		Instructions: "A coding agent is about to start on the prompt below. Pick how it should begin. " +
			"A final line starting with existing-paths: lists the paths the prompt names " +
			"that exist in the repository; a directory ends with a slash.",
		Choices: []decide.Choice{
			{Key: Direct, Description: "The prompt names what to change and where. " +
				"The agent should make the change without surveying the repository."},
			{Key: Explore, Description: "The target is not known from the prompt. " +
				"The agent has to find it before it can change or answer anything."},
			{Key: Coordinate, Description: "The prompt holds several pieces that do not depend on each other. " +
				"The agent should plan them and may hand each to a subagent."},
			{Key: Silent, Description: "None of the others is clearly right, " +
				"or the prompt is conversation and not a task."},
		},
		Fallback: Silent,
	}
}

// Hint returns the sentence handed to the agent for a route, and "" for
// Silent and for any key outside the set.
func Hint(key string) string {
	switch key {
	case Direct:
		return hintDirect
	case Explore:
		return hintExplore
	case Coordinate:
		return hintCoordinate
	default:
		return ""
	}
}

// Decide routes one prompt submitted in the project at root: the rules
// first, then d, then Silent.
func Decide(ctx context.Context, d decide.Decider, root, prompt string) decide.Decision {
	return d.Decide(ctx, Question(), State(root, prompt), Rules()...)
}
