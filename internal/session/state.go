// Package session keeps what one agent session changed in a project: the
// files its edit tools wrote, a baseline of the working tree taken at the
// session's first event, and how the gates last answered. The state lives in
// <root>/.wavez/sessions/<session_id>.json so that each hook invocation, a
// separate process, picks up where the previous one stopped.
package session

import (
	"slices"
)

// Version is the schema of the state file. A file written under any other
// version is discarded and the session starts over with a fresh baseline.
//
// Baseline.Tree and Abstained were added without a new version, on purpose: a new version
// would throw away every live session's baseline, and with it what that
// session changed. A file written before the field existed reads as a
// baseline with no tree, which is exactly what it is, and an older binary
// reading a newer file ignores the key.
const Version = 1

// State is everything remembered about one session.
type State struct {
	SessionID string `json:"session_id"`
	// Passed is the Fingerprint of the change set the gates last passed
	// on. A stop that finds the same fingerprint has nothing to check.
	Passed string `json:"passed,omitempty"`
	// Abstained is the Fingerprint of the change set on which the gates
	// last could not finish: a check timed out or could not run, and none
	// failed. It is not a pass, and it is not run again either, because a
	// check that could not run on these bytes will not run on them a
	// minute later, and the person has been told.
	Abstained string `json:"abstained,omitempty"`
	// Advised identifies the non-blocking text last handed to the agent,
	// so the same advisory is surfaced once.
	Advised  string   `json:"advised,omitempty"`
	Baseline Baseline `json:"baseline"`
	Failure  Failure  `json:"failure"`
	// Goal counts consecutive stops the project's goal held the same way.
	// Its ChangeSet is not used.
	Goal Failure `json:"goal"`
	// Summary identifies the end-of-turn line the person last saw, so the
	// same line is shown once.
	Summary string `json:"summary,omitempty"`
	// Recorded holds the root-relative paths the session's edit tools
	// wrote, sorted and without duplicates.
	Recorded []string `json:"recorded"`
	Version  int      `json:"version"`
	// Chain counts the stops blocked since the agent last stopped of its
	// own accord, whatever they failed on.
	Chain int `json:"chain"`
}

// Baseline is the working tree as the session found it. Dirty maps every
// file that differed from Head at that moment to a hash of its content, and
// to "" when the file was deleted. Outside a git repository Git is false,
// and nothing but Recorded can say what changed unless the project is a jj
// workspace, whose starting working-copy commit JJ names.
type Baseline struct {
	Dirty map[string]string `json:"dirty,omitempty"`
	Head  string            `json:"head,omitempty"`
	// Tree names a git tree object holding the whole working tree as the
	// session found it, uncommitted files included. It is set only when
	// Dirty is not empty, since Head is that tree otherwise, and is left
	// empty when the dirt was too large to keep or could not be stored.
	Tree string `json:"tree,omitempty"`
	// JJ is the commit id of a jj workspace's working-copy commit when the
	// session began, for a workspace git cannot read. Every later change
	// is a diff from it.
	JJ  string `json:"jj,omitempty"`
	Git bool   `json:"git"`
}

// Restorable reports whether the tree the session started from can be
// rebuilt: it is a commit, alone or with the Tree kept for what was
// uncommitted. A session that started outside git, before the first commit,
// or on dirt that was not kept has hashes of its baseline and no content.
func (b Baseline) Restorable() bool {
	return b.Git && b.Head != "" && (len(b.Dirty) == 0 || b.Tree != "")
}

// Failure counts consecutive stops that failed the gates the same way.
type Failure struct {
	// Fingerprint identifies the blocking failures of the last failed run.
	Fingerprint string `json:"fingerprint,omitempty"`
	// ChangeSet is the Fingerprint of the files that last failed run saw.
	ChangeSet string `json:"change_set,omitempty"`
	// Rounds is how many runs in a row produced Fingerprint.
	Rounds int `json:"rounds"`
}

// Record notes that an edit tool wrote the root-relative path.
func (s *State) Record(path string) {
	if path == "" || slices.Contains(s.Recorded, path) {
		return
	}

	s.Recorded = append(s.Recorded, path)
	slices.Sort(s.Recorded)
}

// Fail counts one failed run and returns how many runs in a row have now
// failed with the same fingerprint.
func (s *State) Fail(fingerprint, changeSet string) int {
	if s.Failure.Fingerprint != fingerprint {
		s.Failure = Failure{Fingerprint: fingerprint}
	}

	s.Failure.Rounds++
	s.Failure.ChangeSet = changeSet

	return s.Failure.Rounds
}

// Pass records that the gates passed on the change set and clears the
// failure count.
func (s *State) Pass(changeSet string) {
	s.Passed, s.Abstained = changeSet, ""
	s.Failure = Failure{}
}

// Abstain records that the gates neither failed nor finished on the change
// set, and clears the failure count. What last passed stays what it was.
func (s *State) Abstain(changeSet string) {
	s.Abstained = changeSet
	s.Failure = Failure{}
}
