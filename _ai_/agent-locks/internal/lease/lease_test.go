package lease

import (
	"testing"
	"time"
)

func TestOverlaps(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"internal/api", "internal/api", true},
		{"internal/api", "internal/api/v2", true},
		{"internal/api/v2", "internal/api", true},
		{"internal/api", "internal/store", false},
		{"internal/api", "internal/apiv2", false},
		{".", "internal/api", true},
		{"cmd", "cmdline", false},
	}
	for _, tc := range cases {
		if got := Overlaps(tc.a, tc.b); got != tc.want {
			t.Errorf("Overlaps(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestStrength(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	ttl := 30 * time.Minute

	recent := Lease{Last: now.Add(-time.Minute)}
	if got := recent.Strength(now, time.Time{}, ttl); got != StrengthActive {
		t.Errorf("recent write = %q, want %q", got, StrengthActive)
	}
	if got := recent.Strength(now, now.Add(-30*time.Second), ttl); got != StrengthCommitted {
		t.Errorf("write before a commit = %q, want %q", got, StrengthCommitted)
	}
	stale := Lease{Last: now.Add(-2 * time.Hour)}
	if got := stale.Strength(now, time.Time{}, ttl); got != StrengthExpired {
		t.Errorf("stale write = %q, want %q", got, StrengthExpired)
	}
	manual := Lease{Last: now.Add(-72 * time.Hour), Manual: true}
	if got := manual.Strength(now, now, ttl); got != StrengthManual {
		t.Errorf("manual claim = %q, want %q", got, StrengthManual)
	}
}
