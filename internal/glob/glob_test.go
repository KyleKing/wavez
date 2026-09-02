package glob_test

import (
	"testing"

	"github.com/kyleking/wavez/internal/glob"
)

func TestMatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		pattern string
		rel     string
		want    bool
	}{
		// A bare name matches at any depth, which is what `*.py` means to
		// anyone who writes it.
		{"*.py", "app.py", true},
		{"*.py", "apps/api/deep/app.py", true},
		{"*.py", "app.ts", false},

		// `**` spans any number of segments, including none. path.Match
		// reads it as one `*`, so every case below but the two-level one
		// was false before this package existed.
		{"apps/api/**/*.py", "apps/api/main.py", true},
		{"apps/api/**/*.py", "apps/api/v2/main.py", true},
		{"apps/api/**/*.py", "apps/api/v2/routes/main.py", true},
		{"apps/api/**/*.py", "apps/web/main.py", false},
		{"apps/**", "apps/api/v2/main.py", true},
		{"apps/**", "services/api/main.py", false},

		// A `*` stays inside its segment.
		{"apps/*/main.py", "apps/api/main.py", true},
		{"apps/*/main.py", "apps/api/v2/main.py", false},

		// A path pattern is matched whole, not by base name.
		{"docs/*.md", "README.md", false},
		{"docs/*.md", "docs/README.md", true},

		{"", "anything", true},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+" vs "+tt.rel, func(t *testing.T) {
			t.Parallel()

			if got := glob.Match(tt.pattern, tt.rel); got != tt.want {
				t.Errorf("Match(%q, %q) = %v, want %v", tt.pattern, tt.rel, got, tt.want)
			}
		})
	}
}
