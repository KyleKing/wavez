package gate

import "time"

// CadenceConfig bounds how long a Runner may keep selecting narrow test
// sets before forcing a full run, per DESIGN.md's Gates section ("Full run
// on a cadence: after N selective passes, after a time threshold, or when
// the map flags an untracked file").
type CadenceConfig struct {
	MaxSelectivePasses int
	MaxInterval        time.Duration
}

// NeedsFullRun reports whether the next run must run every test rather
// than a Select-narrowed subset. An untracked file always forces one,
// since the coverage map cannot say anything about a file it has never
// seen; otherwise the pass-count or time threshold decides.
func NeedsFullRun(cfg CadenceConfig, passesSinceFull int, sinceLastFull time.Duration, untrackedFile bool) bool {
	if untrackedFile {
		return true
	}
	if cfg.MaxSelectivePasses > 0 && passesSinceFull >= cfg.MaxSelectivePasses {
		return true
	}
	if cfg.MaxInterval > 0 && sinceLastFull >= cfg.MaxInterval {
		return true
	}

	return false
}
