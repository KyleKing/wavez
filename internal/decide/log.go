package decide

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Permissions for what the package writes under .wavez. State excerpts can
// hold a person's prompt, so nothing is group or world readable.
const (
	dirMode  = 0o750
	fileMode = 0o600
)

const (
	// How much of the state a log line keeps, in bytes.
	logStateLimit = 500
	// The size past which the log is rotated. With the one rotated file
	// kept beside it, a project holds between one and two megabytes of its
	// newest decisions.
	maxLogBytes = 1 << 20
	// What names the one older file a rotation keeps.
	rotatedSuffix = ".1"
)

// record is one line of the decision log: the decision, when it was made,
// and what identifies the state it was made from.
//
//nolint:govet // fieldalignment: the field order is the JSON key order of the decision log
type record struct {
	Time time.Time `json:"time"`
	Decision
	// StateSHA256 is the hex hash of the whole state, so lines over the
	// same state can be joined when the excerpt was cut.
	StateSHA256 string `json:"state_sha256"`
	// State is the start of the state, cut to about 500 bytes.
	State string `json:"state"`
}

// appendLog adds the decision to the log at path. Every failure is
// dropped: the log is for a person later and never for the caller now.
//
// Each record is one write to a descriptor opened O_APPEND, which the
// kernel places whole at the end of the file, so hooks running at once
// never interleave. Rotation is a rename, and a writer that already holds
// the old file finishes into it harmlessly. Two processes rotating at the
// same instant can drop the older rotated file, and nothing newer.
func appendLog(path string, out Decision, state string) {
	sum := sha256.Sum256([]byte(state))

	line, err := json.Marshal(record{
		Time:        time.Now().UTC(),
		Decision:    out,
		StateSHA256: hex.EncodeToString(sum[:]),
		State:       truncate(state, logStateLimit),
	})
	if err != nil {
		return
	}

	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return
	}

	if info, err := os.Stat(path); err == nil && info.Size() >= maxLogBytes {
		os.Rename(path, path+rotatedSuffix) //nolint:errcheck,gosec // a log that cannot rotate is appended to as before
	}

	//nolint:gosec // the path is the caller's own log
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, fileMode)
	if err != nil {
		return
	}

	f.Write(append(line, '\n')) //nolint:errcheck,gosec // a lost log line never changes a decision
	f.Close()                   //nolint:errcheck,gosec // same
}
