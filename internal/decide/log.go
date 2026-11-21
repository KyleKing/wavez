package decide

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// logStateLimit is how much of the state a log line keeps, in bytes. It is
// enough to label the line by eye when the log becomes a corpus.
const logStateLimit = 500

// Record is one line of the decision log: the decision, when it was made,
// and what identifies the state it was made from.
//
//nolint:govet // fieldalignment: the field order is the JSON key order of the decision log
type Record struct {
	Time time.Time `json:"time"`
	Decision
	// StateSHA256 is the hex hash of the whole state, so lines over the
	// same state can be joined when the excerpt was cut.
	StateSHA256 string `json:"state_sha256"`
	// State is the start of the state, cut to about 500 bytes.
	State string `json:"state"`
}

// record appends the decision to the log. Every failure is dropped: the
// log is for a person later and never for the caller now.
func (d *decider) record(out Decision, state string) {
	if d.log == "" {
		return
	}

	sum := sha256.Sum256([]byte(state))

	line, err := json.Marshal(Record{
		Time:        d.now().UTC(),
		Decision:    out,
		StateSHA256: hex.EncodeToString(sum[:]),
		State:       truncate(state, logStateLimit),
	})
	if err != nil {
		return
	}

	if err := os.MkdirAll(filepath.Dir(d.log), dirMode); err != nil {
		return
	}

	f, err := os.OpenFile(d.log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, fileMode)
	if err != nil {
		return
	}

	// One write per line keeps concurrent hooks from interleaving.
	f.Write(append(line, '\n')) //nolint:errcheck,gosec // a lost log line never changes a decision
	f.Close()                   //nolint:errcheck,gosec // same
}
