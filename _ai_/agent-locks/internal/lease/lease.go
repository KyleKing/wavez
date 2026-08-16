package lease

import (
	"path/filepath"
	"strings"
	"time"
)

const (
	StrengthActive    = "active"
	StrengthCommitted = "committed"
	StrengthManual    = "manual"
	StrengthExpired   = "expired"
)

type Lease struct {
	Root    string    `json:"root"`
	Dir     string    `json:"dir"`
	Actor   string    `json:"actor"`
	Owner   string    `json:"owner"`
	Session string    `json:"session,omitempty"`
	Agent   string    `json:"agent,omitempty"`
	Label   string    `json:"label,omitempty"`
	First   time.Time `json:"first"`
	Last    time.Time `json:"last"`
	Writes  int       `json:"writes"`
	Manual  bool      `json:"manual"`
}

func Key(root, dir, actor string) string {
	return root + "\x00" + dir + "\x00" + actor
}

func (l Lease) Key() string { return Key(l.Root, l.Dir, l.Actor) }

// Strength grades a lease against the TTL and the root's last commit. A subtree
// written before the most recent commit is no longer a concurrent-edit risk, only a
// rebase risk, so it degrades rather than expiring outright.
func (l Lease) Strength(now time.Time, lastCommit time.Time, ttl time.Duration) string {
	if l.Manual {
		return StrengthManual
	}
	if now.Sub(l.Last) > ttl {
		return StrengthExpired
	}
	if !lastCommit.IsZero() && l.Last.Before(lastCommit) {
		return StrengthCommitted
	}
	return StrengthActive
}

// Overlaps reports whether two subtree keys contend, which includes the ancestor and
// descendant cases, not only an exact match.
func Overlaps(a, b string) bool {
	if a == b {
		return true
	}
	if a == "." || b == "." {
		return true
	}
	return strings.HasPrefix(a, b+string(filepath.Separator)) ||
		strings.HasPrefix(b, a+string(filepath.Separator))
}
