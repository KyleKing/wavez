package link_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kyleking/wavez/internal/config"
	"github.com/kyleking/wavez/internal/link"
)

func TestCompile_InvalidPatternNamesItInTheError(t *testing.T) {
	t.Parallel()

	_, err := link.Compile([]link.Source{{Pattern: `#(\d+`, URL: "https://example.com/$1"}})
	if err == nil {
		t.Fatal("Compile() = nil error, want one naming the invalid pattern")
	}
	if !strings.Contains(err.Error(), `#(\d+`) {
		t.Errorf("Compile() error = %q, want it to name the invalid pattern", err.Error())
	}
}

func TestTable_LinkifyAndMarkdown(t *testing.T) {
	t.Parallel()

	tbl, err := link.Compile([]link.Source{
		{Pattern: `#(\d+)`, URL: "https://github.com/kyleking/wavez/pull/$1"},
		{Pattern: `\b(ENG-\d+)\b`, URL: "https://linear.app/team/issue/$1"},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	tests := []struct {
		name    string
		in      string
		wantURL string
		wantMD  string
	}{
		{
			name:    "pr number",
			in:      "fixes #123 today",
			wantURL: "https://github.com/kyleking/wavez/pull/123",
			wantMD:  "fixes [#123](https://github.com/kyleking/wavez/pull/123) today",
		},
		{
			name:    "ticket id",
			in:      "see ENG-456 for context",
			wantURL: "https://linear.app/team/issue/ENG-456",
			wantMD:  "see [ENG-456](https://linear.app/team/issue/ENG-456) for context",
		},
		{
			name:   "no match",
			in:     "nothing to link here",
			wantMD: "nothing to link here",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tbl.Markdown(tc.in); got != tc.wantMD {
				t.Errorf("Markdown(%q) = %q, want %q", tc.in, got, tc.wantMD)
			}

			got := tbl.Linkify(tc.in)
			if tc.wantURL == "" {
				if got != tc.in {
					t.Errorf("Linkify(%q) = %q, want unchanged", tc.in, got)
				}

				return
			}
			if !strings.Contains(got, "\x1b]8;;"+tc.wantURL+"\x1b\\") {
				t.Errorf("Linkify(%q) = %q, want it to open a hyperlink to %q", tc.in, got, tc.wantURL)
			}
			if !strings.Contains(got, "\x1b]8;;\x1b\\") {
				t.Errorf("Linkify(%q) = %q, want it to close the hyperlink", tc.in, got)
			}
		})
	}
}

func TestTable_OverlappingMatchesLinkEachSpanOnce(t *testing.T) {
	t.Parallel()

	// Two patterns that can both match "#123": the second is a strict
	// superset of the first's span. Only one link should wrap it.
	tbl, err := link.Compile([]link.Source{
		{Pattern: `#(\d+)`, URL: "https://example.com/narrow/$1"},
		{Pattern: `#\d+`, URL: "https://example.com/wide"},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	got := tbl.Markdown("see #123 please")
	want := "see [#123](https://example.com/narrow/123) please"
	if got != want {
		t.Errorf("Markdown() = %q, want %q (first pattern wins, no double-link)", got, want)
	}
}

func TestTable_RepoPrecedesUserOnAClash(t *testing.T) {
	t.Parallel()

	// FromConfig's output is prepended ahead of the user sources by
	// LoadAll, so simulate that ordering directly: repo pattern first.
	repo := link.FromConfig([]config.LinkPattern{
		{Pattern: `#(\d+)`, URL: "https://github.com/kyleking/wavez/pull/$1"},
	})
	user := []link.Source{
		{Pattern: `#(\d+)`, URL: "https://example.com/personal/$1"},
	}

	tbl, err := link.Compile(append(repo, user...))
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}

	got := tbl.Markdown("#7")
	want := "[#7](https://github.com/kyleking/wavez/pull/7)"
	if got != want {
		t.Errorf("Markdown() = %q, want the repo pattern's URL %q", got, want)
	}
}

func TestLoadAll_MergesRepoAndUserWithRepoFirst(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")

	writeFile(t, filepath.Join(root, config.FileName), `
amends ".wavez/Wavez.pkl"

links {
  new LinkPattern { pattern = "#(\\d+)"; url = "https://github.com/kyleking/wavez/pull/$1" }
}
`)

	userPath, err := link.UserPath()
	if err != nil {
		t.Fatalf("UserPath: %v", err)
	}

	writeFile(t, userPath, `[{"pattern": "\\b(ENG-\\d+)\\b", "url": "https://linear.app/team/issue/$1"}]`)

	tbl, err := link.LoadAll(context.Background(), root)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	got := tbl.Markdown("fixes #9, tracked as ENG-1")
	want := "fixes [#9](https://github.com/kyleking/wavez/pull/9), tracked as " +
		"[ENG-1](https://linear.app/team/issue/ENG-1)"
	if got != want {
		t.Errorf("Markdown() = %q, want %q", got, want)
	}
}

func TestLoadAll_InvalidRepoPatternFailsLoudly(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")

	writeFile(t, filepath.Join(root, config.FileName), `
amends ".wavez/Wavez.pkl"

links {
  new LinkPattern { pattern = "#(\\d+"; url = "https://example.com/$1" }
}
`)

	_, err := link.LoadAll(context.Background(), root)
	if err == nil {
		t.Fatal("LoadAll() = nil error, want one naming the invalid pattern")
	}
	if !strings.Contains(err.Error(), `#(\d+`) {
		t.Errorf("LoadAll() error = %q, want it to name the invalid pattern", err.Error())
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}
