package decide

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

// Permissions for what the package writes under .wavez. State excerpts can
// hold a person's prompt, so nothing is group or world readable.
const (
	dirMode  = 0o750
	fileMode = 0o600
)

// cacheEntry is one stored model answer.
type cacheEntry struct {
	Question string `json:"question"`
	Choice   string `json:"choice"`
	Backend  string `json:"backend"`
	Model    string `json:"model,omitempty"`
}

// cacheKey hashes everything that could change a model's answer. Each part
// is length-prefixed by a separator no part contains after hashing, so two
// different splits of the same bytes never collide.
func cacheKey(q Question, backend, model, state string) string {
	h := sha256.New()

	// Two parts per choice, plus the five fixed ones.
	parts := make([]string, 0, 2*len(q.Choices)+5)
	parts = append(parts, q.Name, q.Instructions, backend, model)

	for _, c := range q.Choices {
		parts = append(parts, c.Key, c.Description)
	}

	parts = append(parts, state)

	for _, part := range parts {
		sum := sha256.Sum256([]byte(part))
		h.Write(sum[:])
	}

	return hex.EncodeToString(h.Sum(nil))
}

// cached returns the stored answer for key. Anything short of a readable
// entry naming a member of the set is a miss.
func (d *decider) cached(q Question, key string) (cacheEntry, bool) {
	if d.cache == "" {
		return cacheEntry{}, false
	}

	//nolint:gosec // the directory is the caller's and the name is a hash computed here
	data, err := os.ReadFile(filepath.Join(d.cache, key+".json"))
	if err != nil {
		return cacheEntry{}, false
	}

	var entry cacheEntry
	if err := json.Unmarshal(data, &entry); err != nil || !q.has(entry.Choice) {
		return cacheEntry{}, false
	}

	return entry, true
}

// store writes entry under key through a rename, so a reader never sees
// half of one. A failure costs the next call a model request and nothing
// else, so it is dropped.
func (d *decider) store(key string, entry cacheEntry) {
	if d.cache == "" {
		return
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return
	}

	if err := os.MkdirAll(d.cache, dirMode); err != nil {
		return
	}

	tmp, err := os.CreateTemp(d.cache, key+".*.tmp")
	if err != nil {
		return
	}

	_, writeErr := tmp.Write(append(data, '\n'))
	closeErr := tmp.Close()

	if writeErr == nil && closeErr == nil && os.Rename(tmp.Name(), filepath.Join(d.cache, key+".json")) == nil {
		return
	}

	os.Remove(tmp.Name()) //nolint:errcheck,gosec // best effort: a stray temp file is never read as an entry
}
