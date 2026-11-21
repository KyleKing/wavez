package gate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	cacheDir = ".wavez/cache"
	// The passDir holds one empty file per custom check run that passed,
	// named for everything that run depended on.
	passDir = "checks"
	// The baselineDir holds what a baseline tree said when it was asked.
	baselineDir = "baseline"
	// The cacheSchema is part of every key, so a change to what an entry
	// means orphans the old entries instead of misreading them.
	cacheSchema = "1"

	cacheDirPerm  = 0o750
	cacheFilePerm = 0o600
)

// NoCacheEnv names the environment variable that, set to anything but "" or
// "0", makes a run neither read nor write the caches under .wavez/cache.
const NoCacheEnv = "WAVEZ_NO_CACHE"

// cache is the project's .wavez/cache. Every failure to read is a miss and
// every failure to write is ignored: a cache that cannot be used costs a
// run its shortcut and nothing else.
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

// has reports whether an entry exists.
func (c cache) has(kind, key string) bool {
	return !c.off && isFile(c.path(kind, key))
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
// half of one. A nil value writes an empty entry, for has.
func (c cache) store(kind, key string, value any) {
	if c.off {
		return
	}

	var raw []byte

	if value != nil {
		encoded, err := json.Marshal(value)
		if err != nil {
			return
		}

		raw = encoded
	}

	dir := filepath.Dir(c.path(kind, key))
	if !c.prepare(dir) {
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

// prepare creates dir and makes the cache ignore itself, so that a project
// that never ran `wavez init` does not find the cache in its git status.
func (c cache) prepare(dir string) bool {
	if err := os.MkdirAll(dir, cacheDirPerm); err != nil {
		return false
	}

	ignore := filepath.Join(c.root, filepath.FromSlash(cacheDir), ".gitignore")
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

// passKey names a custom check run by what its answer depends on: the
// check, its commands, and the bytes of the files it was handed. It is ""
// for a run whose pass must not be reused.
//
// Only a command that names {files} qualifies. One that does not is handed
// nothing, so it reads whatever it likes from the project (`npm test`), and
// the selected files' bytes say too little about what it saw.
func passKey(root string, c customCheck) string {
	if !strings.Contains(c.check.Command, filesToken) || len(c.files) == 0 {
		return ""
	}

	files := slices.Sorted(slices.Values(c.files))
	hashes := hashFiles(root, files)
	parts := []string{"pass", c.check.Name, c.check.Command, c.check.Fix}

	for _, f := range files {
		sum, ok := hashes[f]
		if !ok {
			return ""
		}

		parts = append(parts, f, hex.EncodeToString(sum[:]))
	}

	return cacheKey(parts...)
}

// cacheDisabled reads the opt-out from the environment.
func cacheDisabled() bool {
	v := os.Getenv(NoCacheEnv)

	return v != "" && v != "0"
}
