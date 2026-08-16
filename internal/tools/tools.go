// Package tools implements the v0.1 tool set the chat loop hands to a model:
// read, str_replace, write, shell, search, and question. Each tool is
// constructed with explicit dependencies (a project root, a gate, a store)
// and holds no package-level state.
package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrPathOutsideRoot reports a tool argument naming a path outside the
// project root.
var ErrPathOutsideRoot = errors.New("path is outside the project root")

// resolvePath resolves path against root and refuses one that lexically
// escapes it, whether given as an absolute path or a relative one that
// walks out with "..".
func resolvePath(root, path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("%w: empty path", ErrPathOutsideRoot)
	}

	var abs string
	if filepath.IsAbs(path) {
		abs = filepath.Clean(path)
	} else {
		abs = filepath.Clean(filepath.Join(root, path))
	}

	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %s", ErrPathOutsideRoot, path)
	}

	return abs, nil
}

const shortHashLen = 8

func contentHash(data []byte) string {
	sum := sha256.Sum256(data)

	return hex.EncodeToString(sum[:])
}

func shortHash(hash string) string {
	if len(hash) < shortHashLen {
		return hash
	}

	return hash[:shortHashLen]
}
