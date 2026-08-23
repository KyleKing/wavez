package tool

import "fmt"

// Cause names why a tool call failed. It exists because the rate a tool
// errors at says nothing on its own: a `delete` that refuses a declaration
// still in use is the safeguard working, and one that could not find the
// declaration is a defect, and both were recorded identically until this
// existed. Across this project's first 77 recorded runs, edit tools errored
// on roughly half their calls with no way to tell those apart.
//
// The set is closed and small on purpose. A cause is for aiming work at a
// class of failure, so a taxonomy with a bucket per message would answer
// nothing.
type Cause string

// Causes a tool result may carry.
const (
	// CauseUnspecified is an error from a call site that has not been
	// classified yet. It is a gap in the taxonomy rather than a kind of
	// failure, and a report says so.
	CauseUnspecified Cause = ""
	// CauseNoMatch is a target that is not there: text that does not occur
	// in the file, a symbol nothing declares, a search with no hits.
	CauseNoMatch Cause = "no_match"
	// CauseAmbiguous is several candidates where the call named one.
	CauseAmbiguous Cause = "ambiguous"
	// CauseBadInput is arguments this tool cannot read: absent required
	// fields, a value out of range, JSON the model did not close.
	CauseBadInput Cause = "bad_input"
	// CauseRefused is the tool declining by design. It is the safeguard
	// working, so a report counts it apart from every other cause and a
	// rising refusal rate is not by itself a regression.
	CauseRefused Cause = "refused"
	// CauseConflict is the tree or another writer disagreeing: a held
	// lease, overlapping edits, a file that moved under the call.
	CauseConflict Cause = "conflict"
	// CauseIO is the filesystem or a subprocess failing.
	CauseIO Cause = "io"
	// CauseUpstream is something outside this process saying no: a language
	// server, a network endpoint, a tool it shells out to.
	CauseUpstream Cause = "upstream"
	// CauseMalformed is arguments that never parsed as JSON, so no tool ran
	// at all. It is separate from CauseBadInput because the fix is the
	// model's emission rather than the values it chose, and because it is
	// the failure a small local tier produces most.
	CauseMalformed Cause = "malformed"
	// CauseRepeat is the loop refusing a call identical to one that already
	// failed. It says the run is stuck rather than that the call was wrong,
	// and counting it as a tool failure hides both.
	CauseRepeat Cause = "repeat"
)

// Fail builds an error Result carrying why it failed.
func Fail(cause Cause, format string, args ...any) Result {
	return Result{Content: fmt.Sprintf(format, args...), IsError: true, Cause: cause}
}
