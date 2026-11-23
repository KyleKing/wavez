package gate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const (
	cacheDir = ".wavez/cache"
	// The baselineDir holds what a baseline tree said when it was asked.
	baselineDir = "baseline"
	// The lintCacheDir is golangci-lint's own cache, kept per project.
	lintCacheDir = "golangci-lint"
	// The cacheSchema is part of every key, so a change to what an entry
	// means orphans the old entries instead of misreading them.
	cacheSchema = "2"

	cacheDirPerm  = 0o750
	cacheFilePerm = 0o600
)

// NoCacheEnv names the environment variable that, set to anything but "" or
// "0", makes a run neither read nor write what the baseline tree said under
// .wavez/cache.
const NoCacheEnv = "WAVEZ_NO_CACHE"

// cache is the project's .wavez/cache. Every failure to read is a miss and
// every failure to write is ignored: a cache that cannot be used costs a
// run its shortcut and nothing else. It holds only what a baseline tree
// said, which is a fact about a tree that no longer changes. A check's pass
// on the working tree is never kept here: its answer depends on files the
// key cannot see, and the session already remembers the change set it
// passed on.
type cache struct {
	root string
	off  bool
}

// cacheKey hashes parts into a file name. Each part is length-prefixed by
// its separator, so no two lists of parts share a key.
func cacheKey(parts ...string) string {
	sum := sha256.New()
	sum.Write([]byte(cacheSchema))

	for _, part := range parts {
		sum.Write([]byte{0})
		sum.Write([]byte(hex.EncodeToString([]byte(part))))
	}

	return hex.EncodeToString(sum.Sum(nil))
}

func (c cache) path(kind, key string) string {
	return filepath.Join(c.root, filepath.FromSlash(cacheDir), kind, key)
}

// load decodes an entry into out. An entry that is missing or does not
// decode is a miss.
func (c cache) load(kind, key string, out any) bool {
	if c.off {
		return false
	}

	raw, err := os.ReadFile(c.path(kind, key))

	return err == nil && json.Unmarshal(raw, out) == nil
}

// store writes an entry through a temporary file, so a reader never sees
// half of one.
func (c cache) store(kind, key string, value any) {
	if c.off {
		return
	}

	raw, err := json.Marshal(value)
	if err != nil {
		return
	}

	dir := filepath.Dir(c.path(kind, key))
	if !prepareCache(c.root, dir) {
		return
	}

	tmp, err := os.CreateTemp(dir, "tmp-*")
	if err != nil {
		return
	}

	_, err = tmp.Write(raw)
	if err = errors.Join(err, tmp.Close()); err == nil {
		err = os.Rename(tmp.Name(), c.path(kind, key))
	}

	if err != nil {
		discard(tmp.Name())
	}
}

// prepareCache creates dir under root's cache and makes the cache ignore
// itself, so that a project that never ran `wavez init` does not find the
// cache in its git status.
func prepareCache(root, dir string) bool {
	if err := os.MkdirAll(dir, cacheDirPerm); err != nil {
		return false
	}

	ignore := filepath.Join(root, filepath.FromSlash(cacheDir), ".gitignore")
	if isFile(ignore) {
		return true
	}

	return os.WriteFile(ignore, []byte("*\n"), cacheFilePerm) == nil
}

// discard removes a file whose removal failing changes nothing.
func discard(path string) {
	if err := os.Remove(path); err != nil {
		return
	}
}

// cacheDisabled reads the opt-out from the environment.
func cacheDisabled() bool {
	v := os.Getenv(NoCacheEnv)

	return v != "" && v != "0"
}
