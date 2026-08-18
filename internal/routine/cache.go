package routine

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sync"
)

// Cache holds the compiled Set for one config content hash, DESIGN.md's
// "compiled DAG is a disposable artifact keyed by the pkl content hash.
// Drift means recompile, not patch". It lives in memory rather than on
// disk: what compiling costs is validating and binding actions, and the pkl
// evaluation it would otherwise avoid is ~130 µs warm (measured in
// _ai_/demos/pkl-routines), so a persisted artifact would carry drift risk
// for nothing.
type Cache struct {
	set  *Set
	hash string
	mu   sync.Mutex
}

// Compiled returns the Set for hash, compiling defs when the cached set
// came from different config content.
func (c *Cache) Compiled(hash string, defs map[string]Definition, reg *Registry) (*Set, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.set != nil && c.hash == hash {
		return c.set, nil
	}

	set, err := CompileSet(defs, reg, hash)
	if err != nil {
		return nil, err
	}

	c.set, c.hash = set, hash

	return set, nil
}

// HashFile returns the content hash of the config file at path, and the
// hash of no config at all when the file is absent.
func HashFile(path string) (string, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is a caller-configured project file
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}

	if err != nil {
		return "", fmt.Errorf("hashing %s: %w", path, err)
	}

	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:]), nil
}
