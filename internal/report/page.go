// Package report turns one agent session into a page a person can read in
// seconds: what needs them, where the turns went, and what to say next
// time. It reads a timeline and renders; it runs nothing and scores nothing.
package report

import (
	"github.com/kyleking/wavez/internal/transcript"
)

// NotRecorded stands in for a number the inputs did not hold. A page never
// shows a zero for something nobody measured.
const NotRecorded = "not recorded"

// Goal is how far the session's goal got. The caller fills it from whatever
// tracks the goal; a nil Goal leaves the goal off the page.
type Goal struct {
	// Sentence states the goal.
	Sentence string
	// Unmet names the items not done yet.
	Unmet []string
	Total int
	Done  int
}

// State is what wavez's own session file adds to a transcript.
type State struct {
	// Changed is how many files the session's edit tools wrote.
	Changed int
	// FailingRounds is how many stops in a row last failed the gates the
	// same way, zero when the gates last passed.
	FailingRounds int
}

// Tally is what a hook trace adds: the hints and denials the hooks made.
type Tally struct {
	RouteHints    int
	RenameDenials int
}

// Input is everything a session page is built from.
type Input struct {
	// Goal, State, and Tally are optional.
	Goal  *Goal
	State *State
	Tally *Tally
	// Project names the project on the page.
	Project  string
	Timeline transcript.SessionTimeline
	// Full shows whole commands, prompts, and hook texts, which the
	// timeline must have been read WithDetail to hold.
	Full bool
}

// Page is a session ready to render. Every value is already text, so the
// HTML and plain renderings say the same thing.
type Page struct {
	Goal    *GoalView
	Project string
	// Dir is the directory the session ran in.
	Dir string
	// Session is the whole session id and Short its first eight characters.
	Session  string
	Short    string
	Date     string
	Summary  string
	GateNote string
	Stats    []Fact
	Needs    []Item
	Cells    []Cell
	Shares   []Share
	Legend   []Legend
	Segments []SegmentRow
	Kinds    []KindRow
	Facts    []Fact
	Waste    []Item
	Gates    []GateRow
	Full     bool
}

// Fact is one labeled value.
type Fact struct {
	Label string
	Value string
	// Note qualifies the value in a few words.
	Note string
}

// Item is one thing that needs the person or one piece of waste, with the
// guidance that answers it.
type Item struct {
	// ID names the element holding Copy.
	ID     string
	Title  string
	Detail string
	// Copy is ready to paste into a prompt or a shell, and empty when
	// there is nothing useful to paste.
	Copy string
}

// Cell is one step on the strip.
type Cell struct {
	// Letter is the kind's letter, or the prompt's number.
	Letter string
	// Class is the CSS class for the kind.
	Class string
	// Title says what the step was.
	Title string
	// Detail is the whole command or prompt, on a full page only.
	Detail string
	// Mark is "failed", "denied", "blocked", or empty.
	Mark string
}

// Share is one kind's part of the agent's steps.
type Share struct {
	Letter  string
	Class   string
	Name    string
	Count   string
	Percent string
	// Width is the share as a CSS percentage.
	Width string
}

// Legend explains one mark on the strip.
type Legend struct {
	Letter string
	Class  string
	Name   string
}

// SegmentRow is the work that followed one prompt.
type SegmentRow struct {
	Number string
	Label  string
	Steps  string
	Turns  string
}

// KindRow is one line of the table of steps and tokens by kind.
type KindRow struct {
	Letter string
	Class  string
	Name   string
	Steps  string
	Share  string
	Turns  string
	Output string
	Input  string
	// Total marks the closing row.
	Total bool
}

// GateRow is one run of the Stop hooks.
type GateRow struct {
	Number  string
	At      string
	Outcome string
	// Class is the CSS class for the outcome.
	Class    string
	Checks   string
	Duration string
	Agent    string
	Person   string
	// Said is the first line of what was said, or all of it on a full page.
	Said string
}

// GoalView is the goal as the page shows it.
type GoalView struct {
	Sentence string
	Progress string
	Unmet    []string
}
