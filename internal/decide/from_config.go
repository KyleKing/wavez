package decide

import (
	"os"
	"path/filepath"

	"github.com/kyleking/wavez/internal/config"
)

// Where a project's decision state lives, relative to its root.
const (
	stateDir = ".wavez"
	logFile  = "decisions.jsonl"
)

// LogPath is the decision log FromConfig writes for the project at root.
func LogPath(root string) string {
	return filepath.Join(root, stateDir, logFile)
}

// CacheDir is the answer cache FromConfig keeps for the project at root.
func CacheDir(root string) string {
	return filepath.Join(root, stateDir, "cache", "decide")
}

// FromConfig returns the Decider a project's configuration describes, with
// its cache and its decision log kept under root's .wavez directory. With no
// endpoint configured it has no backend, so every question gets its rule or
// its fallback, and the log still records which. The endpoint's API key,
// when it needs one, is read from the environment variable APIKeyEnv.
//
//nolint:ireturn // Decider is the frozen surface internal/route and internal/hook consume
func FromConfig(root string, cfg config.Decide) Decider {
	opts := []Option{WithLog(LogPath(root))}

	if cfg.Endpoint != "" {
		timeout := cfg.Timeout
		if timeout <= 0 {
			timeout = config.DefaultDecideTimeout
		}

		backend := NewOpenAI(cfg.Endpoint, cfg.Model, timeout, WithAPIKey(os.Getenv(APIKeyEnv)))
		opts = append(opts, WithBackend(backend), WithCache(CacheDir(root)))
	}

	return New(opts...)
}
