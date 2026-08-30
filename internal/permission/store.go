package permission

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// storeFilePerm keeps the approval file readable only by its owner. It
// records what this machine's user has agreed to run unattended, which is
// nobody else's business.
const storeFilePerm = 0o600

// maxStoredKey bounds one recorded key. A command line long enough to pass
// this is not one a person approved deliberately, and refusing to store it
// costs a prompt rather than safety.
const maxStoredKey = 4096

// maxScanLine bounds one line of the file, which holds a key and the small
// record around it.
const maxScanLine = 2 * maxStoredKey

// grant is one persisted approval, written as a line of JSON so the file
// stays append-only and readable by hand.
type grant struct {
	At   time.Time `json:"at"`
	Tool string    `json:"tool"`
	Key  string    `json:"key"`
}

// Store remembers the allow-always answers one project has been given, so
// an approval outlives the thread that gave it.
//
// It is deliberately exact. A key names one whole command line, never the
// program it starts with, so approving one call never approves a different
// one: the file is a list of things this project may do unattended and
// reads as one.
//
// Nothing but an AllowAlways is written. A denial is not recorded, because
// a Store that could refuse would be a policy engine, and the gate in front
// of it already refuses what the guard says to refuse.
type Store struct {
	granted map[string]bool
	path    string
	mu      sync.Mutex
}

// OpenStore reads the project's approvals from dir, creating nothing until
// something is granted. A file that cannot be read is an error: a store
// that silently starts empty would re-ask for everything, which is the safe
// direction, but it would also hide a corrupted file forever.
func OpenStore(dir string) (*Store, error) {
	s := &Store{path: filepath.Join(dir, "approvals.jsonl"), granted: map[string]bool{}}

	f, err := os.Open(s.path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", s.path, err)
	}
	defer f.Close() //nolint:errcheck // read-only

	scanner := bufio.NewScanner(f)
	scanner.Buffer(nil, maxScanLine)

	for scanner.Scan() {
		var g grant
		if err := json.Unmarshal(scanner.Bytes(), &g); err != nil {
			return nil, fmt.Errorf("reading %s: %w", s.path, err)
		}

		if g.Key != "" {
			s.granted[g.Tool+"\x00"+g.Key] = true
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("reading %s: %w", s.path, err)
	}

	return s, nil
}

// Allowed reports whether this project has already approved req's exact
// action.
func (s *Store) Allowed(req Request) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.granted[req.Tool+"\x00"+req.Key]
}

// Remember records req's key as approved from now on.
func (s *Store) Remember(req Request) error {
	if req.Key == "" || len(req.Key) > maxStoredKey {
		return nil
	}

	line, err := json.Marshal(grant{Tool: req.Tool, Key: req.Key, At: time.Now()})
	if err != nil {
		return fmt.Errorf("encoding an approval: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, storeFilePerm)
	if err != nil {
		return fmt.Errorf("opening %s: %w", s.path, err)
	}
	defer f.Close() //nolint:errcheck // the write is checked below

	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("writing %s: %w", s.path, err)
	}

	s.granted[req.Tool+"\x00"+req.Key] = true

	return nil
}

// Persisting wraps gate so an allow-always answer outlives the thread that
// gave it, and a later thread asking the same exact question is answered
// from the store instead of asking again.
//
// The store is consulted before the gate and never after: a request it
// already holds is allowed without a prompt, and everything else is asked
// exactly as before. A store that cannot be written still allows the call
// the user just approved, since refusing the answer they gave would be the
// wrong way to report a full disk.
func Persisting(gate Gate, store *Store) Gate { //nolint:ireturn // a Gate is what the caller wires in
	if store == nil {
		return gate
	}

	return GateFunc(func(ctx context.Context, req Request) (Decision, error) {
		if store.Allowed(req) {
			return Allow, nil
		}

		decision, err := gate.Ask(ctx, req)
		if err != nil {
			return decision, fmt.Errorf("asking the gate: %w", err)
		}

		if decision == AllowAlways {
			if rerr := store.Remember(req); rerr != nil {
				return AllowAlways, nil //nolint:nilerr // the answer stands; the record is what failed
			}
		}

		return decision, nil
	})
}
