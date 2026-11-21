package config_test

import (
	"errors"
	"testing"

	"github.com/kyleking/wavez/internal/config"
)

func TestGlob_Match(t *testing.T) {
	t.Parallel()

	tests := []struct {
		pattern string
		path    string
		want    bool
	}{
		{"**/*.py", "a.py", true},
		{"**/*.py", "src/pkg/a.py", true},
		{"**/*.py", "a.pyc", false},
		{"*.py", "src/a.py", true},
		{"*.py", "a.go", false},
		{"src/*.py", "src/a.py", true},
		{"src/*.py", "src/pkg/a.py", false},
		{"src/**", "src/pkg/a.py", true},
		{"src/**", "lib/a.py", false},
		{"docs/**/*.md", "docs/a.md", true},
		{"docs/**/*.md", "docs/x/y/a.md", true},
		{"go.mod", "go.mod", true},
		{"go.mod", "sub/go.mod", true},
		{"./go.mod", "sub/go.mod", false},
		{"./go.mod", "go.mod", true},
		{"file?.txt", "file1.txt", true},
		{"file?.txt", "file12.txt", false},
		{"[ab].go", "a.go", true},
		{"[!ab].go", "a.go", false},
		{"[!ab].go", "c.go", true},
		{"a+b.go", "a+b.go", true},
		{"a+b.go", "aab.go", false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+" "+tt.path, func(t *testing.T) {
			t.Parallel()

			glob, err := config.ParseGlob(tt.pattern)
			if err != nil {
				t.Fatalf("ParseGlob: %v", err)
			}

			if got := glob.Match(tt.path); got != tt.want {
				t.Errorf("Match(%q) = %v, want %v", tt.path, got, tt.want)
			}

			if glob.String() != tt.pattern {
				t.Errorf("String() = %q", glob.String())
			}
		})
	}
}

func TestParseGlob_RejectsBadPatterns(t *testing.T) {
	t.Parallel()

	for _, pattern := range []string{"", "[a", "src/[z-a].go"} {
		t.Run(pattern, func(t *testing.T) {
			t.Parallel()

			_, err := config.ParseGlob(pattern)

			var bad *config.GlobError
			if !errors.As(err, &bad) || bad.Pattern != pattern {
				t.Errorf("got %v, want a *config.GlobError for %q", err, pattern)
			}
		})
	}

	if (config.Glob{}).Match("a.go") {
		t.Error("the zero Glob matched")
	}
}
